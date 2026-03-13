//! Turn-scoped state and active turn metadata scaffolding.

use indexmap::IndexMap;
use std::collections::HashMap;
use std::collections::VecDeque;
use std::ops::Range;
use std::sync::Arc;
use tokio::sync::Mutex;
use tokio::sync::Notify;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
use tokio_util::task::AbortOnDropHandle;

use codex_protocol::dynamic_tools::DynamicToolResponse;
use codex_protocol::items::ContextCompactionItem;
use codex_protocol::models::ResponseInputItem;
use codex_protocol::models::ResponseItem;
use codex_protocol::request_permissions::RequestPermissionsResponse;
use codex_protocol::request_user_input::RequestUserInputResponse;
use codex_rmcp_client::ElicitationResponse;
use rmcp::model::RequestId;
use tokio::sync::oneshot;

use crate::codex::TurnContext;
use crate::compact::LocalCompactResult;
use crate::compact_remote::RemoteCompactionResult;
use crate::protocol::ReviewDecision;
use crate::protocol::TokenUsage;
use crate::sandboxing::merge_permission_profiles;
use crate::tasks::SessionTask;
use codex_protocol::models::PermissionProfile;

/// Metadata about the currently running turn.
pub(crate) struct ActiveTurn {
    pub(crate) tasks: IndexMap<String, RunningTask>,
    pub(crate) turn_state: Arc<Mutex<TurnState>>,
    background_auto_compactions: Vec<BackgroundAutoCompaction>,
    completed_background_auto_compactions: VecDeque<CompletedBackgroundAutoCompaction>,
    next_background_auto_compaction_launch_ordinal: u64,
}

impl Default for ActiveTurn {
    fn default() -> Self {
        Self {
            tasks: IndexMap::new(),
            turn_state: Arc::new(Mutex::new(TurnState::default())),
            background_auto_compactions: Vec::new(),
            completed_background_auto_compactions: VecDeque::new(),
            next_background_auto_compaction_launch_ordinal: 0,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum TaskKind {
    Regular,
    Review,
    Compact,
}

pub(crate) struct RunningTask {
    pub(crate) done: Arc<Notify>,
    pub(crate) kind: TaskKind,
    pub(crate) task: Arc<dyn SessionTask>,
    pub(crate) cancellation_token: CancellationToken,
    pub(crate) handle: Arc<AbortOnDropHandle<()>>,
    pub(crate) turn_context: Arc<TurnContext>,
    // Timer recorded when the task drops to capture the full turn duration.
    pub(crate) _timer: Option<codex_otel::Timer>,
}

pub(crate) struct BackgroundAutoCompaction {
    pub(crate) snapshot_marker: String,
    pub(crate) snapshot_history_len: usize,
    pub(crate) snapshot_history: Vec<ResponseItem>,
    pub(crate) launch_ordinal: u64,
    pub(crate) compaction_item: ContextCompactionItem,
    pub(crate) failure_notify: Arc<Notify>,
    pub(crate) cancellation_token: CancellationToken,
    pub(crate) handle: JoinHandle<()>,
}

#[derive(Debug)]
pub(crate) enum BackgroundAutoCompactionResult {
    Local(LocalCompactResult),
    Remote(RemoteCompactionResult),
}

#[derive(Debug)]
pub(crate) enum BackgroundAutoCompactionOutcome {
    Succeeded(Box<BackgroundAutoCompactionResult>),
    Failed(String),
}

pub(crate) struct CompletedBackgroundAutoCompaction {
    pub(crate) snapshot_marker: String,
    pub(crate) snapshot_history_len: usize,
    pub(crate) snapshot_history: Vec<ResponseItem>,
    pub(crate) launch_ordinal: u64,
    pub(crate) compaction_item: ContextCompactionItem,
    pub(crate) outcome: BackgroundAutoCompactionOutcome,
}

impl ActiveTurn {
    pub(crate) fn add_task(&mut self, task: RunningTask) {
        let sub_id = task.turn_context.sub_id.clone();
        self.tasks.insert(sub_id, task);
    }

    pub(crate) fn remove_task(&mut self, sub_id: &str) -> bool {
        self.tasks.swap_remove(sub_id);
        self.tasks.is_empty()
    }

    pub(crate) fn drain_tasks(&mut self) -> Vec<RunningTask> {
        self.tasks.drain(..).map(|(_, task)| task).collect()
    }

    pub(crate) fn can_start_background_auto_compaction(
        &self,
        _snapshot_history_len: usize,
    ) -> bool {
        self.background_auto_compactions.is_empty()
            && self.completed_background_auto_compactions.is_empty()
    }

    pub(crate) fn next_background_auto_compaction_launch_ordinal(&mut self) -> u64 {
        let launch_ordinal = self.next_background_auto_compaction_launch_ordinal;
        self.next_background_auto_compaction_launch_ordinal += 1;
        launch_ordinal
    }

    pub(crate) fn insert_completed_background_auto_compaction(
        &mut self,
        completed_background_auto_compaction: CompletedBackgroundAutoCompaction,
    ) {
        let insertion_index = self
            .completed_background_auto_compactions
            .iter()
            .position(|existing_background_auto_compaction| {
                existing_background_auto_compaction.launch_ordinal
                    > completed_background_auto_compaction.launch_ordinal
            })
            .unwrap_or(self.completed_background_auto_compactions.len());
        self.completed_background_auto_compactions
            .insert(insertion_index, completed_background_auto_compaction);
    }

    pub(crate) fn set_background_auto_compaction(
        &mut self,
        background_auto_compaction: BackgroundAutoCompaction,
    ) -> bool {
        if !self
            .can_start_background_auto_compaction(background_auto_compaction.snapshot_history_len)
        {
            return false;
        }
        self.background_auto_compactions
            .push(background_auto_compaction);
        true
    }

    pub(crate) fn finish_background_auto_compaction(
        &mut self,
        snapshot_marker: &str,
        outcome: BackgroundAutoCompactionOutcome,
    ) -> Option<ContextCompactionItem> {
        let background_auto_compaction_index =
            self.background_auto_compactions
                .iter()
                .position(|background_auto_compaction| {
                    background_auto_compaction.snapshot_marker == snapshot_marker
                })?;
        let background_auto_compaction = self
            .background_auto_compactions
            .swap_remove(background_auto_compaction_index);

        let compaction_item = background_auto_compaction.compaction_item;
        self.insert_completed_background_auto_compaction(CompletedBackgroundAutoCompaction {
            snapshot_marker: background_auto_compaction.snapshot_marker,
            snapshot_history_len: background_auto_compaction.snapshot_history_len,
            snapshot_history: background_auto_compaction.snapshot_history,
            launch_ordinal: background_auto_compaction.launch_ordinal,
            compaction_item: compaction_item.clone(),
            outcome,
        });
        Some(compaction_item)
    }

    pub(crate) fn take_background_auto_compaction(
        &mut self,
        snapshot_marker: &str,
    ) -> Option<BackgroundAutoCompaction> {
        let background_auto_compaction_index =
            self.background_auto_compactions
                .iter()
                .position(|background_auto_compaction| {
                    background_auto_compaction.snapshot_marker == snapshot_marker
                })?;
        Some(
            self.background_auto_compactions
                .swap_remove(background_auto_compaction_index),
        )
    }

    pub(crate) fn take_all_background_auto_compactions(&mut self) -> Vec<BackgroundAutoCompaction> {
        self.background_auto_compactions.drain(..).collect()
    }

    pub(crate) fn background_auto_compaction_failure_notifies(&self) -> Vec<Arc<Notify>> {
        self.background_auto_compactions
            .iter()
            .map(|background_auto_compaction| {
                Arc::clone(&background_auto_compaction.failure_notify)
            })
            .collect()
    }

    pub(crate) fn take_completed_background_auto_compaction(
        &mut self,
    ) -> Option<CompletedBackgroundAutoCompaction> {
        self.completed_background_auto_compactions.pop_front()
    }

    pub(crate) fn take_latest_completed_background_auto_compaction(
        &mut self,
    ) -> Option<CompletedBackgroundAutoCompaction> {
        self.completed_background_auto_compactions.pop_back()
    }

    pub(crate) fn has_tracked_background_auto_compaction_newer_than(
        &self,
        launch_ordinal: u64,
    ) -> bool {
        self.background_auto_compactions
            .iter()
            .any(|background_auto_compaction| {
                background_auto_compaction.launch_ordinal > launch_ordinal
            })
            || self.completed_background_auto_compactions.iter().any(
                |completed_background_auto_compaction| {
                    completed_background_auto_compaction.launch_ordinal > launch_ordinal
                },
            )
    }

    pub(crate) fn take_background_auto_compactions_older_than(
        &mut self,
        launch_ordinal: u64,
    ) -> Vec<BackgroundAutoCompaction> {
        let mut older_background_auto_compactions = Vec::new();
        let mut retained_background_auto_compactions = Vec::new();
        for background_auto_compaction in self.background_auto_compactions.drain(..) {
            if background_auto_compaction.launch_ordinal < launch_ordinal {
                older_background_auto_compactions.push(background_auto_compaction);
            } else {
                retained_background_auto_compactions.push(background_auto_compaction);
            }
        }
        self.background_auto_compactions = retained_background_auto_compactions;
        older_background_auto_compactions
    }

    pub(crate) fn clear_completed_background_auto_compactions_older_than(
        &mut self,
        launch_ordinal: u64,
    ) {
        self.completed_background_auto_compactions
            .retain(|completed_background_auto_compaction| {
                completed_background_auto_compaction.launch_ordinal >= launch_ordinal
            });
    }

    #[cfg(test)]
    pub(crate) fn take_successful_completed_background_auto_compaction(
        &mut self,
    ) -> Option<CompletedBackgroundAutoCompaction> {
        let successful_completed_background_auto_compaction_index = self
            .completed_background_auto_compactions
            .iter()
            .position(|completed_background_auto_compaction| {
                matches!(
                    completed_background_auto_compaction,
                    CompletedBackgroundAutoCompaction {
                        outcome: BackgroundAutoCompactionOutcome::Succeeded(_),
                        ..
                    }
                )
            })?;
        self.completed_background_auto_compactions
            .remove(successful_completed_background_auto_compaction_index)
    }

    #[cfg(test)]
    pub(crate) fn take_failed_completed_background_auto_compaction(
        &mut self,
    ) -> Option<CompletedBackgroundAutoCompaction> {
        let failed_completed_background_auto_compaction_index = self
            .completed_background_auto_compactions
            .iter()
            .position(|completed_background_auto_compaction| {
                matches!(
                    completed_background_auto_compaction,
                    CompletedBackgroundAutoCompaction {
                        outcome: BackgroundAutoCompactionOutcome::Failed(_),
                        ..
                    }
                )
            })?;
        self.completed_background_auto_compactions
            .remove(failed_completed_background_auto_compaction_index)
    }

    pub(crate) fn clear_completed_background_auto_compactions(&mut self) {
        while let Some(CompletedBackgroundAutoCompaction {
            snapshot_marker,
            snapshot_history_len,
            snapshot_history,
            launch_ordinal,
            compaction_item,
            outcome,
        }) = self.take_completed_background_auto_compaction()
        {
            let _ = snapshot_marker;
            let _ = snapshot_history_len;
            let _ = snapshot_history;
            let _ = launch_ordinal;
            let _ = compaction_item;
            match outcome {
                BackgroundAutoCompactionOutcome::Succeeded(result) => match *result {
                    BackgroundAutoCompactionResult::Local(result) => {
                        let _ = result;
                    }
                    BackgroundAutoCompactionResult::Remote(result) => {
                        let _ = result;
                    }
                },
                BackgroundAutoCompactionOutcome::Failed(message) => {
                    let _ = message;
                }
            }
        }
    }
}

/// Mutable state for a single turn.
#[derive(Default)]
pub(crate) struct TurnState {
    pending_approvals: HashMap<String, oneshot::Sender<ReviewDecision>>,
    pending_request_permissions: HashMap<String, oneshot::Sender<RequestPermissionsResponse>>,
    pending_user_input: HashMap<String, oneshot::Sender<RequestUserInputResponse>>,
    pending_elicitations: HashMap<(String, RequestId), oneshot::Sender<ElicitationResponse>>,
    pending_dynamic_tools: HashMap<String, oneshot::Sender<DynamicToolResponse>>,
    pending_input: Vec<ResponseInputItem>,
    granted_permissions: Option<PermissionProfile>,
    pub(crate) scrolling_context: Option<ScrollingContextState>,
    pub(crate) tool_calls: u64,
    pub(crate) token_usage_at_turn_start: TokenUsage,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct ScrollingContextState {
    pub(crate) injected_ranges: Vec<Range<usize>>,
    pub(crate) scroll_range: Option<Range<usize>>,
}

impl TurnState {
    pub(crate) fn insert_pending_approval(
        &mut self,
        key: String,
        tx: oneshot::Sender<ReviewDecision>,
    ) -> Option<oneshot::Sender<ReviewDecision>> {
        self.pending_approvals.insert(key, tx)
    }

    pub(crate) fn remove_pending_approval(
        &mut self,
        key: &str,
    ) -> Option<oneshot::Sender<ReviewDecision>> {
        self.pending_approvals.remove(key)
    }

    pub(crate) fn clear_pending(&mut self) {
        self.pending_approvals.clear();
        self.pending_request_permissions.clear();
        self.pending_user_input.clear();
        self.pending_elicitations.clear();
        self.pending_dynamic_tools.clear();
        self.pending_input.clear();
    }

    pub(crate) fn insert_pending_request_permissions(
        &mut self,
        key: String,
        tx: oneshot::Sender<RequestPermissionsResponse>,
    ) -> Option<oneshot::Sender<RequestPermissionsResponse>> {
        self.pending_request_permissions.insert(key, tx)
    }

    pub(crate) fn remove_pending_request_permissions(
        &mut self,
        key: &str,
    ) -> Option<oneshot::Sender<RequestPermissionsResponse>> {
        self.pending_request_permissions.remove(key)
    }

    pub(crate) fn insert_pending_user_input(
        &mut self,
        key: String,
        tx: oneshot::Sender<RequestUserInputResponse>,
    ) -> Option<oneshot::Sender<RequestUserInputResponse>> {
        self.pending_user_input.insert(key, tx)
    }

    pub(crate) fn remove_pending_user_input(
        &mut self,
        key: &str,
    ) -> Option<oneshot::Sender<RequestUserInputResponse>> {
        self.pending_user_input.remove(key)
    }

    pub(crate) fn insert_pending_elicitation(
        &mut self,
        server_name: String,
        request_id: RequestId,
        tx: oneshot::Sender<ElicitationResponse>,
    ) -> Option<oneshot::Sender<ElicitationResponse>> {
        self.pending_elicitations
            .insert((server_name, request_id), tx)
    }

    pub(crate) fn remove_pending_elicitation(
        &mut self,
        server_name: &str,
        request_id: &RequestId,
    ) -> Option<oneshot::Sender<ElicitationResponse>> {
        self.pending_elicitations
            .remove(&(server_name.to_string(), request_id.clone()))
    }

    pub(crate) fn insert_pending_dynamic_tool(
        &mut self,
        key: String,
        tx: oneshot::Sender<DynamicToolResponse>,
    ) -> Option<oneshot::Sender<DynamicToolResponse>> {
        self.pending_dynamic_tools.insert(key, tx)
    }

    pub(crate) fn remove_pending_dynamic_tool(
        &mut self,
        key: &str,
    ) -> Option<oneshot::Sender<DynamicToolResponse>> {
        self.pending_dynamic_tools.remove(key)
    }

    pub(crate) fn push_pending_input(&mut self, input: ResponseInputItem) {
        self.pending_input.push(input);
    }

    pub(crate) fn take_pending_input(&mut self) -> Vec<ResponseInputItem> {
        if self.pending_input.is_empty() {
            Vec::with_capacity(0)
        } else {
            let mut ret = Vec::new();
            std::mem::swap(&mut ret, &mut self.pending_input);
            ret
        }
    }

    pub(crate) fn has_pending_input(&self) -> bool {
        !self.pending_input.is_empty()
    }

    pub(crate) fn record_granted_permissions(&mut self, permissions: PermissionProfile) {
        self.granted_permissions =
            merge_permission_profiles(self.granted_permissions.as_ref(), Some(&permissions));
    }

    pub(crate) fn granted_permissions(&self) -> Option<PermissionProfile> {
        self.granted_permissions.clone()
    }
}

impl ActiveTurn {
    /// Clear any pending approvals and input buffered for the current turn.
    pub(crate) async fn clear_pending(&self) {
        let mut ts = self.turn_state.lock().await;
        ts.clear_pending();
    }
}
