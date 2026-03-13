use async_trait::async_trait;
use serde::Deserialize;
use serde::Serialize;
use std::ops::Range;

use crate::function_tool::FunctionCallError;
use crate::scrolling_context::DEFAULT_SEARCH_LIMIT;
use crate::scrolling_context::MAX_MATCH_CONTEXT_ITEMS;
use crate::scrolling_context::MAX_SCROLL_PAGES;
use crate::scrolling_context::MAX_SEARCH_LIMIT;
use crate::scrolling_context::ScrollDirection;
use crate::scrolling_context::scroll_window;
use crate::scrolling_context::search_window;
use crate::state::ScrollingContextState;
use crate::tools::context::FunctionToolOutput;
use crate::tools::context::ToolInvocation;
use crate::tools::context::ToolPayload;
use crate::tools::handlers::parse_arguments;
use crate::tools::registry::ToolHandler;
use crate::tools::registry::ToolKind;
use codex_protocol::config_types::HistoryContextMode;
use codex_protocol::models::ReasoningItemReasoningSummary;
use codex_protocol::models::ResponseItem;

pub(crate) const CONVERSATION_HISTORY_TOOL_NAME: &str = "conversation_history";

const DEFAULT_SCROLL_PAGES: usize = 1;

fn default_scroll_pages() -> usize {
    DEFAULT_SCROLL_PAGES
}

fn default_search_limit() -> usize {
    DEFAULT_SEARCH_LIMIT
}

fn default_before_matches() -> usize {
    1
}

fn default_after_matches() -> usize {
    1
}

#[derive(Deserialize)]
#[serde(tag = "action", rename_all = "snake_case")]
enum ConversationHistoryArgs {
    Scroll {
        direction: ScrollDirectionArg,
        #[serde(default = "default_scroll_pages")]
        pages: usize,
    },
    Search {
        query: String,
        #[serde(default = "default_search_limit")]
        limit: usize,
        #[serde(default = "default_before_matches")]
        before_matches: usize,
        #[serde(default = "default_after_matches")]
        after_matches: usize,
    },
}

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "snake_case")]
enum ScrollDirectionArg {
    Backward,
    Forward,
}

impl From<ScrollDirectionArg> for ScrollDirection {
    fn from(value: ScrollDirectionArg) -> Self {
        match value {
            ScrollDirectionArg::Backward => ScrollDirection::Backward,
            ScrollDirectionArg::Forward => ScrollDirection::Forward,
        }
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct RangePayload {
    start_index: usize,
    end_index_exclusive: usize,
    preview: Vec<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SearchHitPayload {
    prompt_index: usize,
    score: f32,
    preview: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ScrollResponse {
    action: &'static str,
    pages: usize,
    injected_range: Option<RangePayload>,
    visible_ranges: Vec<RangePayload>,
    has_more_older: bool,
    has_more_newer: bool,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SearchResponse {
    action: &'static str,
    query: String,
    limit: usize,
    before_matches: usize,
    after_matches: usize,
    hits: Vec<SearchHitPayload>,
    visible_ranges: Vec<RangePayload>,
}

pub struct ConversationHistoryHandler;

#[async_trait]
impl ToolHandler for ConversationHistoryHandler {
    type Output = FunctionToolOutput;

    fn kind(&self) -> ToolKind {
        ToolKind::Function
    }

    async fn handle(&self, invocation: ToolInvocation) -> Result<Self::Output, FunctionCallError> {
        let ToolInvocation {
            session,
            turn,
            payload,
            ..
        } = invocation;

        if turn.history_context_mode != HistoryContextMode::ScrollingWindow {
            return Err(FunctionCallError::RespondToModel(
                "conversation_history is only available when `history_context_mode = \"scrolling_window\"`"
                    .to_string(),
            ));
        }

        let arguments = match payload {
            ToolPayload::Function { arguments } => arguments,
            _ => {
                return Err(FunctionCallError::Fatal(format!(
                    "{CONVERSATION_HISTORY_TOOL_NAME} handler received unsupported payload"
                )));
            }
        };
        let args: ConversationHistoryArgs = parse_arguments(&arguments)?;
        let full_prompt = session
            .clone_history()
            .await
            .for_prompt(&turn.model_info.input_modalities);
        let scrolling_context = session.scrolling_context_state(&turn.sub_id).await;

        match args {
            ConversationHistoryArgs::Scroll { direction, pages } => {
                let pages = pages.clamp(1, MAX_SCROLL_PAGES);
                let result = scroll_window(
                    &full_prompt,
                    scrolling_context.as_ref(),
                    direction.into(),
                    pages,
                );
                session
                    .set_scrolling_context_state(turn.as_ref(), result.state.clone())
                    .await;
                session.recompute_token_usage(turn.as_ref()).await;
                let response = ScrollResponse {
                    action: "scroll",
                    pages,
                    injected_range: result
                        .injected_range
                        .as_ref()
                        .map(|range| range_payload(&full_prompt, range)),
                    visible_ranges: state_ranges(&full_prompt, result.state.as_ref()),
                    has_more_older: result.has_more_older,
                    has_more_newer: result.has_more_newer,
                };
                serialize_response(response)
            }
            ConversationHistoryArgs::Search {
                query,
                limit,
                before_matches,
                after_matches,
            } => {
                let query = query.trim();
                if query.is_empty() {
                    return Err(FunctionCallError::RespondToModel(
                        "query must not be empty".to_string(),
                    ));
                }

                let limit = limit.clamp(1, MAX_SEARCH_LIMIT);
                let before_matches = before_matches.min(MAX_MATCH_CONTEXT_ITEMS);
                let after_matches = after_matches.min(MAX_MATCH_CONTEXT_ITEMS);
                let result =
                    search_window(&full_prompt, query, limit, before_matches, after_matches);
                session
                    .set_scrolling_context_state(turn.as_ref(), result.state.clone())
                    .await;
                session.recompute_token_usage(turn.as_ref()).await;
                let response = SearchResponse {
                    action: "search",
                    query: query.to_string(),
                    limit,
                    before_matches,
                    after_matches,
                    hits: result
                        .hits
                        .into_iter()
                        .map(|hit| SearchHitPayload {
                            prompt_index: hit.prompt_index,
                            score: hit.score,
                            preview: hit.preview,
                        })
                        .collect(),
                    visible_ranges: state_ranges(&full_prompt, result.state.as_ref()),
                };
                serialize_response(response)
            }
        }
    }
}

fn serialize_response<T: Serialize>(response: T) -> Result<FunctionToolOutput, FunctionCallError> {
    let content = serde_json::to_string(&response).map_err(|err| {
        FunctionCallError::Fatal(format!(
            "failed to serialize {CONVERSATION_HISTORY_TOOL_NAME} response: {err}"
        ))
    })?;
    Ok(FunctionToolOutput::from_text(content, Some(true)))
}

fn state_ranges(
    full_prompt: &[ResponseItem],
    state: Option<&ScrollingContextState>,
) -> Vec<RangePayload> {
    state
        .map(|state| {
            state
                .injected_ranges
                .iter()
                .map(|range| range_payload(full_prompt, range))
                .collect()
        })
        .unwrap_or_default()
}

fn range_payload(full_prompt: &[ResponseItem], range: &Range<usize>) -> RangePayload {
    RangePayload {
        start_index: range.start,
        end_index_exclusive: range.end,
        preview: range_preview(full_prompt, range),
    }
}

fn range_preview(full_prompt: &[ResponseItem], range: &Range<usize>) -> Vec<String> {
    full_prompt[range.clone()]
        .iter()
        .filter_map(response_item_preview)
        .take(3)
        .collect()
}

fn response_item_preview(item: &ResponseItem) -> Option<String> {
    match item {
        ResponseItem::Message { role, content, .. } => {
            let text = crate::compact::content_items_to_text(content)?;
            let text = text.trim();
            (!text.is_empty()).then_some(format!("{role}: {text}"))
        }
        ResponseItem::Reasoning { summary, .. } => {
            let text = summary
                .iter()
                .map(|summary| match summary {
                    ReasoningItemReasoningSummary::SummaryText { text } => text.as_str(),
                })
                .collect::<Vec<_>>()
                .join(" ");
            let text = text.trim();
            (!text.is_empty()).then_some(format!("reasoning: {text}"))
        }
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use codex_protocol::models::ContentItem;
    use pretty_assertions::assert_eq;

    fn message(role: &str, text: &str) -> ResponseItem {
        ResponseItem::Message {
            id: None,
            role: role.to_string(),
            content: vec![ContentItem::InputText {
                text: text.to_string(),
            }],
            end_turn: None,
            phase: None,
        }
    }

    #[test]
    fn range_preview_uses_message_text() {
        let prompt = vec![
            message("user", "alpha"),
            message("assistant", "beta"),
            message("assistant", "gamma"),
            message("assistant", "delta"),
        ];

        assert_eq!(
            range_preview(&prompt, &(1..4)),
            vec![
                "assistant: beta".to_string(),
                "assistant: gamma".to_string(),
                "assistant: delta".to_string(),
            ]
        );
    }
}
