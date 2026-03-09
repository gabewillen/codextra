use anyhow::Context;
use anyhow::Result;
use app_test_support::McpProcess;
use app_test_support::create_final_assistant_message_sse_response;
use app_test_support::create_mock_responses_server_sequence_unchecked;
use app_test_support::to_response;
use codex_app_server_protocol::JSONRPCNotification;
use codex_app_server_protocol::JSONRPCResponse;
use codex_app_server_protocol::RequestId;
use codex_app_server_protocol::ThreadCompactStartParams;
use codex_app_server_protocol::ThreadCompactStartResponse;
use codex_app_server_protocol::ThreadItem;
use codex_app_server_protocol::ThreadReadParams;
use codex_app_server_protocol::ThreadReadResponse;
use codex_app_server_protocol::ThreadResumeParams;
use codex_app_server_protocol::ThreadResumeResponse;
use codex_app_server_protocol::ThreadRollbackParams;
use codex_app_server_protocol::ThreadRollbackResponse;
use codex_app_server_protocol::ThreadStartParams;
use codex_app_server_protocol::ThreadStartResponse;
use codex_app_server_protocol::ThreadStatus;
use codex_app_server_protocol::TurnCompletedNotification;
use codex_app_server_protocol::TurnStartParams;
use codex_app_server_protocol::TurnStartResponse;
use codex_app_server_protocol::UserInput as V2UserInput;
use pretty_assertions::assert_eq;
use serde_json::Value;
use tempfile::TempDir;
use tokio::time::timeout;

const DEFAULT_READ_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(10);

#[tokio::test]
async fn thread_rollback_drops_last_turns_and_persists_to_rollout() -> Result<()> {
    // Three Codex turns hit the mock model (session start + two turn/start calls).
    let responses = vec![
        create_final_assistant_message_sse_response("Done")?,
        create_final_assistant_message_sse_response("Done")?,
        create_final_assistant_message_sse_response("Done")?,
    ];
    let server = create_mock_responses_server_sequence_unchecked(responses).await;

    let codex_home = TempDir::new()?;
    create_config_toml(codex_home.path(), &server.uri())?;

    let mut mcp = McpProcess::new(codex_home.path()).await?;
    timeout(DEFAULT_READ_TIMEOUT, mcp.initialize()).await??;

    // Start a thread.
    let start_id = mcp
        .send_thread_start_request(ThreadStartParams {
            model: Some("mock-model".to_string()),
            ..Default::default()
        })
        .await?;
    let start_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(start_id)),
    )
    .await??;
    let ThreadStartResponse { thread, .. } = to_response::<ThreadStartResponse>(start_resp)?;

    // Two turns.
    let first_text = "First";
    let turn1_id = mcp
        .send_turn_start_request(TurnStartParams {
            thread_id: thread.id.clone(),
            input: vec![V2UserInput::Text {
                text: first_text.to_string(),
                text_elements: Vec::new(),
            }],
            ..Default::default()
        })
        .await?;
    let _turn1_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(turn1_id)),
    )
    .await??;
    let _completed1 = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_notification_message("turn/completed"),
    )
    .await??;

    let turn2_id = mcp
        .send_turn_start_request(TurnStartParams {
            thread_id: thread.id.clone(),
            input: vec![V2UserInput::Text {
                text: "Second".to_string(),
                text_elements: Vec::new(),
            }],
            ..Default::default()
        })
        .await?;
    let _turn2_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(turn2_id)),
    )
    .await??;
    let _completed2 = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_notification_message("turn/completed"),
    )
    .await??;

    // Roll back the last turn.
    let rollback_id = mcp
        .send_thread_rollback_request(ThreadRollbackParams {
            thread_id: thread.id.clone(),
            num_turns: 1,
        })
        .await?;
    let rollback_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(rollback_id)),
    )
    .await??;
    let rollback_result = rollback_resp.result.clone();
    let ThreadRollbackResponse {
        thread: rolled_back_thread,
    } = to_response::<ThreadRollbackResponse>(rollback_resp)?;

    // Wire contract: thread title field is `name`, serialized as null when unset.
    let thread_json = rollback_result
        .get("thread")
        .and_then(Value::as_object)
        .expect("thread/rollback result.thread must be an object");
    assert_eq!(rolled_back_thread.name, None);
    assert_eq!(
        thread_json.get("name"),
        Some(&Value::Null),
        "thread/rollback must serialize `name: null` when unset"
    );

    assert_eq!(rolled_back_thread.turns.len(), 1);
    assert_eq!(rolled_back_thread.status, ThreadStatus::Idle);
    assert_eq!(rolled_back_thread.turns[0].items.len(), 2);
    match &rolled_back_thread.turns[0].items[0] {
        ThreadItem::UserMessage { content, .. } => {
            assert_eq!(
                content,
                &vec![V2UserInput::Text {
                    text: first_text.to_string(),
                    text_elements: Vec::new(),
                }]
            );
        }
        other => panic!("expected user message item, got {other:?}"),
    }

    // Resume and confirm the history is pruned.
    let resume_id = mcp
        .send_thread_resume_request(ThreadResumeParams {
            thread_id: thread.id,
            ..Default::default()
        })
        .await?;
    let resume_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(resume_id)),
    )
    .await??;
    let ThreadResumeResponse { thread, .. } = to_response::<ThreadResumeResponse>(resume_resp)?;

    assert_eq!(thread.turns.len(), 1);
    assert_eq!(thread.status, ThreadStatus::Idle);
    assert_eq!(thread.turns[0].items.len(), 2);
    match &thread.turns[0].items[0] {
        ThreadItem::UserMessage { content, .. } => {
            assert_eq!(
                content,
                &vec![V2UserInput::Text {
                    text: first_text.to_string(),
                    text_elements: Vec::new(),
                }]
            );
        }
        other => panic!("expected user message item, got {other:?}"),
    }

    Ok(())
}

#[tokio::test]
async fn thread_rollback_preserves_prior_compaction_turns() -> Result<()> {
    let responses = vec![
        create_final_assistant_message_sse_response("Done")?,
        create_final_assistant_message_sse_response("Manual summary")?,
        create_final_assistant_message_sse_response("Done again")?,
    ];
    let server = create_mock_responses_server_sequence_unchecked(responses).await;

    let codex_home = TempDir::new()?;
    create_config_toml(codex_home.path(), &server.uri())?;

    let mut mcp = McpProcess::new(codex_home.path()).await?;
    timeout(DEFAULT_READ_TIMEOUT, mcp.initialize()).await??;

    let start_id = mcp
        .send_thread_start_request(ThreadStartParams {
            model: Some("mock-model".to_string()),
            ..Default::default()
        })
        .await?;
    let start_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(start_id)),
    )
    .await??;
    let ThreadStartResponse { thread, .. } = to_response::<ThreadStartResponse>(start_resp)?;

    let turn1_id = mcp
        .send_turn_start_request(TurnStartParams {
            thread_id: thread.id.clone(),
            input: vec![V2UserInput::Text {
                text: "First".to_string(),
                text_elements: Vec::new(),
            }],
            ..Default::default()
        })
        .await?;
    let turn1_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(turn1_id)),
    )
    .await??;
    let TurnStartResponse { turn: turn1 } = to_response::<TurnStartResponse>(turn1_resp)?;
    wait_for_turn_completed(&mut mcp, &turn1.id).await?;

    let compact_id = mcp
        .send_thread_compact_start_request(ThreadCompactStartParams {
            thread_id: thread.id.clone(),
        })
        .await?;
    let compact_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(compact_id)),
    )
    .await??;
    let _compact: ThreadCompactStartResponse =
        to_response::<ThreadCompactStartResponse>(compact_resp)?;
    wait_for_compaction_in_thread_history(&mut mcp, &thread.id).await?;

    let turn2_id = mcp
        .send_turn_start_request(TurnStartParams {
            thread_id: thread.id.clone(),
            input: vec![V2UserInput::Text {
                text: "Second".to_string(),
                text_elements: Vec::new(),
            }],
            ..Default::default()
        })
        .await?;
    let turn2_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(turn2_id)),
    )
    .await??;
    let TurnStartResponse { turn: turn2 } = to_response::<TurnStartResponse>(turn2_resp)?;
    wait_for_turn_completed(&mut mcp, &turn2.id).await?;

    let rollback_id = mcp
        .send_thread_rollback_request(ThreadRollbackParams {
            thread_id: thread.id.clone(),
            num_turns: 1,
        })
        .await?;
    let rollback_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(rollback_id)),
    )
    .await??;
    let ThreadRollbackResponse {
        thread: rolled_back_thread,
    } = to_response::<ThreadRollbackResponse>(rollback_resp)?;
    assert!(thread_contains_context_compaction(
        &rolled_back_thread.turns
    ));

    let resume_id = mcp
        .send_thread_resume_request(ThreadResumeParams {
            thread_id: thread.id,
            ..Default::default()
        })
        .await?;
    let resume_resp: JSONRPCResponse = timeout(
        DEFAULT_READ_TIMEOUT,
        mcp.read_stream_until_response_message(RequestId::Integer(resume_id)),
    )
    .await??;
    let ThreadResumeResponse {
        thread: resumed_thread,
        ..
    } = to_response::<ThreadResumeResponse>(resume_resp)?;
    assert!(thread_contains_context_compaction(&resumed_thread.turns));

    Ok(())
}

fn create_config_toml(codex_home: &std::path::Path, server_uri: &str) -> std::io::Result<()> {
    let config_toml = codex_home.join("config.toml");
    std::fs::write(
        config_toml,
        format!(
            r#"
model = "mock-model"
approval_policy = "never"
sandbox_mode = "read-only"

model_provider = "mock_provider"

[model_providers.mock_provider]
name = "Mock provider for test"
base_url = "{server_uri}/v1"
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
"#
        ),
    )
}

async fn wait_for_turn_completed(mcp: &mut McpProcess, turn_id: &str) -> Result<()> {
    loop {
        let notification: JSONRPCNotification = timeout(
            DEFAULT_READ_TIMEOUT,
            mcp.read_stream_until_notification_message("turn/completed"),
        )
        .await??;
        let params = notification
            .params
            .context("turn/completed notification missing params")?;
        let completed: TurnCompletedNotification = serde_json::from_value(params)?;
        if completed.turn.id == turn_id {
            return Ok(());
        }
    }
}

async fn wait_for_compaction_in_thread_history(
    mcp: &mut McpProcess,
    thread_id: &str,
) -> Result<()> {
    loop {
        let read_id = mcp
            .send_thread_read_request(ThreadReadParams {
                thread_id: thread_id.to_string(),
                include_turns: true,
            })
            .await?;
        let read_resp: JSONRPCResponse = timeout(
            DEFAULT_READ_TIMEOUT,
            mcp.read_stream_until_response_message(RequestId::Integer(read_id)),
        )
        .await??;
        let ThreadReadResponse { thread } = to_response::<ThreadReadResponse>(read_resp)?;
        if thread_contains_context_compaction(&thread.turns) {
            return Ok(());
        }
    }
}

fn thread_contains_context_compaction(turns: &[codex_app_server_protocol::Turn]) -> bool {
    turns.iter().any(|turn| {
        turn.items
            .iter()
            .any(|item| matches!(item, ThreadItem::ContextCompaction { .. }))
    })
}
