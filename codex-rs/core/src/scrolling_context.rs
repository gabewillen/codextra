use std::collections::BTreeSet;
use std::collections::HashMap;
use std::ops::Range;

use bm25::Document;
use bm25::Language;
use bm25::SearchEngineBuilder;
use codex_protocol::config_types::HistoryContextMode;
use codex_protocol::models::BaseInstructions;
use codex_protocol::models::ReasoningItemReasoningSummary;
use codex_protocol::models::ResponseItem;

use crate::codex::TurnContext;
use crate::compact::content_items_to_text;
use crate::context_manager::ContextManager;
use crate::state::ScrollingContextState;
use crate::truncate::TruncationPolicy;
use crate::truncate::truncate_text;

pub(crate) const DEFAULT_NEWEST_WINDOW_ITEMS: usize = 24;
pub(crate) const DEFAULT_SCROLL_PAGE_ITEMS: usize = 12;
pub(crate) const DEFAULT_SEARCH_LIMIT: usize = 3;
pub(crate) const MAX_SCROLL_PAGES: usize = 5;
pub(crate) const MAX_SEARCH_LIMIT: usize = 8;
pub(crate) const MAX_MATCH_CONTEXT_ITEMS: usize = 8;
const PREVIEW_TEXT_MAX_CHARS: usize = 120;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ScrollDirection {
    Backward,
    Forward,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ScrollWindowResult {
    pub(crate) state: Option<ScrollingContextState>,
    pub(crate) injected_range: Option<Range<usize>>,
    pub(crate) has_more_older: bool,
    pub(crate) has_more_newer: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) struct SearchHit {
    pub(crate) prompt_index: usize,
    pub(crate) score: f32,
    pub(crate) preview: String,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) struct SearchWindowResult {
    pub(crate) state: Option<ScrollingContextState>,
    pub(crate) hits: Vec<SearchHit>,
}

pub(crate) fn build_prompt_items(
    history: ContextManager,
    turn_context: &TurnContext,
    scrolling_context: Option<&ScrollingContextState>,
) -> Vec<ResponseItem> {
    let full_prompt = history.for_prompt(&turn_context.model_info.input_modalities);
    if turn_context.history_context_mode != HistoryContextMode::ScrollingWindow {
        return full_prompt;
    }
    apply_scrolling_window(&full_prompt, scrolling_context)
}

pub(crate) fn estimate_prompt_token_count(
    history: ContextManager,
    turn_context: &TurnContext,
    scrolling_context: Option<&ScrollingContextState>,
    base_instructions: &BaseInstructions,
) -> Option<i64> {
    let prompt_items = build_prompt_items(history, turn_context, scrolling_context);
    let mut prompt_context = ContextManager::new();
    prompt_context.replace(prompt_items);
    prompt_context.estimate_token_count_with_base_instructions(base_instructions)
}

pub(crate) fn scroll_window(
    full_prompt: &[ResponseItem],
    scrolling_context: Option<&ScrollingContextState>,
    direction: ScrollDirection,
    pages: usize,
) -> ScrollWindowResult {
    let tail_start = newest_window_start(full_prompt.len());
    if tail_start == 0 {
        return ScrollWindowResult {
            state: None,
            injected_range: None,
            has_more_older: false,
            has_more_newer: false,
        };
    }

    let page_span = DEFAULT_SCROLL_PAGE_ITEMS.saturating_mul(pages.max(1));
    let current_scroll_range = scrolling_context.and_then(|state| state.scroll_range.clone());
    let injected_range = match (direction, current_scroll_range) {
        (ScrollDirection::Backward, Some(range)) => {
            let end = range.start.min(tail_start);
            let start = end.saturating_sub(page_span);
            (start < end).then_some(start..end)
        }
        (ScrollDirection::Backward, None) => {
            let end = tail_start;
            let start = end.saturating_sub(page_span);
            (start < end).then_some(start..end)
        }
        (ScrollDirection::Forward, Some(range)) => {
            let start = range.end.min(tail_start);
            let end = start.saturating_add(page_span).min(tail_start);
            (start < end).then_some(start..end)
        }
        (ScrollDirection::Forward, None) => None,
    };

    let has_more_older = injected_range.as_ref().is_some_and(|range| range.start > 0);
    let has_more_newer = injected_range
        .as_ref()
        .is_some_and(|range| range.end < tail_start);

    ScrollWindowResult {
        state: injected_range.clone().map(|range| ScrollingContextState {
            injected_ranges: std::iter::once(range.clone()).collect(),
            scroll_range: Some(range),
        }),
        injected_range,
        has_more_older,
        has_more_newer,
    }
}

pub(crate) fn search_window(
    full_prompt: &[ResponseItem],
    query: &str,
    limit: usize,
    before_matches: usize,
    after_matches: usize,
) -> SearchWindowResult {
    let tail_start = newest_window_start(full_prompt.len());
    if tail_start == 0 {
        return SearchWindowResult {
            state: None,
            hits: Vec::new(),
        };
    }

    let documents: Vec<(usize, Document<usize>, String)> = full_prompt[..tail_start]
        .iter()
        .enumerate()
        .filter_map(|(prompt_index, item)| {
            let search_text = searchable_text(item)?;
            let preview = build_preview(&search_text);
            Some((
                prompt_index,
                Document::new(prompt_index, search_text),
                preview,
            ))
        })
        .collect();
    if documents.is_empty() {
        return SearchWindowResult {
            state: None,
            hits: Vec::new(),
        };
    }

    let search_documents = documents
        .iter()
        .map(|(_, document, _)| document.clone())
        .collect::<Vec<Document<usize>>>();
    let search_engine =
        SearchEngineBuilder::<usize>::with_documents(Language::English, search_documents).build();

    let hits = search_engine
        .search(query, limit)
        .into_iter()
        .filter_map(|result| {
            documents
                .iter()
                .find(|(prompt_index, _, _)| *prompt_index == result.document.id)
                .map(|(prompt_index, _, preview)| SearchHit {
                    prompt_index: *prompt_index,
                    score: result.score,
                    preview: preview.clone(),
                })
        })
        .collect::<Vec<_>>();

    let injected_ranges = merge_ranges(
        hits.iter()
            .map(|hit| {
                let start = hit.prompt_index.saturating_sub(before_matches);
                let end = hit
                    .prompt_index
                    .saturating_add(after_matches.saturating_add(1))
                    .min(tail_start);
                start..end
            })
            .filter(|range| range.start < range.end)
            .collect(),
    );

    SearchWindowResult {
        state: (!injected_ranges.is_empty()).then_some(ScrollingContextState {
            injected_ranges,
            scroll_range: None,
        }),
        hits,
    }
}

pub(crate) fn newest_window_start(prompt_len: usize) -> usize {
    prompt_len.saturating_sub(DEFAULT_NEWEST_WINDOW_ITEMS)
}

fn apply_scrolling_window(
    full_prompt: &[ResponseItem],
    scrolling_context: Option<&ScrollingContextState>,
) -> Vec<ResponseItem> {
    if full_prompt.len() <= DEFAULT_NEWEST_WINDOW_ITEMS && scrolling_context.is_none() {
        return full_prompt.to_vec();
    }

    let tail_start = newest_window_start(full_prompt.len());
    let mut ranges = scrolling_context
        .map(|state| {
            state
                .injected_ranges
                .iter()
                .map(|range| range.start.min(full_prompt.len())..range.end.min(full_prompt.len()))
                .filter(|range| range.start < range.end)
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    ranges.push(tail_start..full_prompt.len());

    let merged_ranges = merge_ranges(ranges);
    let mut selected_indexes: BTreeSet<usize> = merged_ranges
        .iter()
        .flat_map(std::clone::Clone::clone)
        .collect();
    include_call_pairs(full_prompt, &mut selected_indexes);

    selected_indexes
        .into_iter()
        .map(|index| full_prompt[index].clone())
        .collect()
}

fn merge_ranges(mut ranges: Vec<Range<usize>>) -> Vec<Range<usize>> {
    if ranges.is_empty() {
        return ranges;
    }
    ranges.sort_by_key(|range| (range.start, range.end));

    let mut merged: Vec<Range<usize>> = Vec::with_capacity(ranges.len());
    for range in ranges {
        if let Some(last) = merged.last_mut()
            && range.start <= last.end
        {
            last.end = last.end.max(range.end);
        } else {
            merged.push(range);
        }
    }
    merged
}

fn include_call_pairs(full_prompt: &[ResponseItem], selected_indexes: &mut BTreeSet<usize>) {
    let mut function_like_calls: HashMap<&str, usize> = HashMap::new();
    let mut function_outputs: HashMap<&str, usize> = HashMap::new();
    let mut custom_calls: HashMap<&str, usize> = HashMap::new();
    let mut custom_outputs: HashMap<&str, usize> = HashMap::new();

    for (index, item) in full_prompt.iter().enumerate() {
        match item {
            ResponseItem::FunctionCall { call_id, .. } => {
                function_like_calls.insert(call_id.as_str(), index);
            }
            ResponseItem::LocalShellCall {
                call_id: Some(call_id),
                ..
            } => {
                function_like_calls.insert(call_id.as_str(), index);
            }
            ResponseItem::LocalShellCall { call_id: None, .. } => {}
            ResponseItem::FunctionCallOutput { call_id, .. } => {
                function_outputs.insert(call_id.as_str(), index);
            }
            ResponseItem::CustomToolCall { call_id, .. } => {
                custom_calls.insert(call_id.as_str(), index);
            }
            ResponseItem::CustomToolCallOutput { call_id, .. } => {
                custom_outputs.insert(call_id.as_str(), index);
            }
            ResponseItem::Message { .. }
            | ResponseItem::Reasoning { .. }
            | ResponseItem::WebSearchCall { .. }
            | ResponseItem::ImageGenerationCall { .. }
            | ResponseItem::Compaction { .. }
            | ResponseItem::GhostSnapshot { .. }
            | ResponseItem::Other => {}
        }
    }

    let originally_selected = selected_indexes.iter().copied().collect::<Vec<_>>();
    for index in originally_selected {
        match &full_prompt[index] {
            ResponseItem::FunctionCall { call_id, .. }
            | ResponseItem::FunctionCallOutput { call_id, .. } => {
                if let Some(call_index) = function_like_calls.get(call_id.as_str()) {
                    selected_indexes.insert(*call_index);
                }
                if let Some(output_index) = function_outputs.get(call_id.as_str()) {
                    selected_indexes.insert(*output_index);
                }
            }
            ResponseItem::LocalShellCall {
                call_id: Some(call_id),
                ..
            } => {
                if let Some(output_index) = function_outputs.get(call_id.as_str()) {
                    selected_indexes.insert(*output_index);
                }
            }
            ResponseItem::LocalShellCall { call_id: None, .. } => {}
            ResponseItem::CustomToolCall { call_id, .. }
            | ResponseItem::CustomToolCallOutput { call_id, .. } => {
                if let Some(call_index) = custom_calls.get(call_id.as_str()) {
                    selected_indexes.insert(*call_index);
                }
                if let Some(output_index) = custom_outputs.get(call_id.as_str()) {
                    selected_indexes.insert(*output_index);
                }
            }
            ResponseItem::Message { .. }
            | ResponseItem::Reasoning { .. }
            | ResponseItem::WebSearchCall { .. }
            | ResponseItem::ImageGenerationCall { .. }
            | ResponseItem::Compaction { .. }
            | ResponseItem::GhostSnapshot { .. }
            | ResponseItem::Other => {}
        }
    }
}

fn searchable_text(item: &ResponseItem) -> Option<String> {
    match item {
        ResponseItem::Message { role, content, .. } if role == "user" || role == "assistant" => {
            content_items_to_text(content)
        }
        ResponseItem::Reasoning { summary, .. } => {
            let text = summary
                .iter()
                .map(|summary| match summary {
                    ReasoningItemReasoningSummary::SummaryText { text } => text.as_str(),
                })
                .collect::<Vec<_>>()
                .join(" ");
            (!text.trim().is_empty()).then_some(text)
        }
        _ => None,
    }
}

fn build_preview(text: &str) -> String {
    truncate_text(text.trim(), TruncationPolicy::Bytes(PREVIEW_TEXT_MAX_CHARS))
}

#[cfg(test)]
mod tests {
    use pretty_assertions::assert_eq;

    use super::*;
    use codex_protocol::models::ContentItem;
    use codex_protocol::models::FunctionCallOutputPayload;

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

    fn prompt_texts(prompt: &[ResponseItem]) -> Vec<String> {
        prompt.iter().filter_map(searchable_text).collect()
    }

    fn function_call(call_id: &str, name: &str) -> ResponseItem {
        ResponseItem::FunctionCall {
            id: None,
            name: name.to_string(),
            arguments: "{}".to_string(),
            call_id: call_id.to_string(),
        }
    }

    fn function_call_output(call_id: &str, text: &str) -> ResponseItem {
        ResponseItem::FunctionCallOutput {
            call_id: call_id.to_string(),
            output: FunctionCallOutputPayload::from_text(text.to_string()),
        }
    }

    #[test]
    fn scroll_window_moves_back_from_newest_boundary() {
        let prompt = (0..42)
            .map(|index| message("user", &format!("message {index}")))
            .collect::<Vec<_>>();

        let result = scroll_window(&prompt, None, ScrollDirection::Backward, 1);

        assert_eq!(result.injected_range, Some(6..18));
        assert_eq!(
            result.state,
            Some(ScrollingContextState {
                injected_ranges: std::iter::once(6..18).collect(),
                scroll_range: Some(6..18),
            })
        );
        assert!(result.has_more_older);
        assert!(!result.has_more_newer);
    }

    #[test]
    fn search_window_merges_overlapping_match_contexts() {
        let prompt = vec![
            message("user", "alpha start"),
            message("assistant", "alpha middle"),
            message("user", "alpha end"),
            message("assistant", "tail 1"),
            message("assistant", "tail 2"),
            message("assistant", "tail 3"),
            message("assistant", "tail 4"),
            message("assistant", "tail 5"),
            message("assistant", "tail 6"),
            message("assistant", "tail 7"),
            message("assistant", "tail 8"),
            message("assistant", "tail 9"),
            message("assistant", "tail 10"),
            message("assistant", "tail 11"),
            message("assistant", "tail 12"),
            message("assistant", "tail 13"),
            message("assistant", "tail 14"),
            message("assistant", "tail 15"),
            message("assistant", "tail 16"),
            message("assistant", "tail 17"),
            message("assistant", "tail 18"),
            message("assistant", "tail 19"),
            message("assistant", "tail 20"),
            message("assistant", "tail 21"),
            message("assistant", "tail 22"),
            message("assistant", "tail 23"),
            message("assistant", "tail 24"),
        ];

        let result = search_window(&prompt, "alpha", 3, 1, 1);

        assert_eq!(result.hits.len(), 3);
        assert_eq!(
            result.state,
            Some(ScrollingContextState {
                injected_ranges: std::iter::once(0..3).collect(),
                scroll_range: None,
            })
        );
    }

    #[test]
    fn apply_scrolling_window_keeps_newest_window_and_injected_slice() {
        let prompt = (0..30)
            .map(|index| message("user", &format!("message {index}")))
            .collect::<Vec<_>>();

        let visible_prompt = apply_scrolling_window(
            &prompt,
            Some(&ScrollingContextState {
                injected_ranges: std::iter::once(2..4).collect(),
                scroll_range: Some(2..4),
            }),
        );

        assert_eq!(
            prompt_texts(&visible_prompt),
            vec![
                "message 2".to_string(),
                "message 3".to_string(),
                "message 6".to_string(),
                "message 7".to_string(),
                "message 8".to_string(),
                "message 9".to_string(),
                "message 10".to_string(),
                "message 11".to_string(),
                "message 12".to_string(),
                "message 13".to_string(),
                "message 14".to_string(),
                "message 15".to_string(),
                "message 16".to_string(),
                "message 17".to_string(),
                "message 18".to_string(),
                "message 19".to_string(),
                "message 20".to_string(),
                "message 21".to_string(),
                "message 22".to_string(),
                "message 23".to_string(),
                "message 24".to_string(),
                "message 25".to_string(),
                "message 26".to_string(),
                "message 27".to_string(),
                "message 28".to_string(),
                "message 29".to_string(),
            ]
        );
    }

    #[test]
    fn apply_scrolling_window_keeps_function_call_with_selected_output() {
        let mut prompt = vec![
            message("user", "message 0"),
            function_call("call-1", "tool"),
        ];
        prompt.push(function_call_output("call-1", "tool result"));
        prompt.extend((3..26).map(|index| message("user", &format!("message {index}"))));

        let visible_prompt = apply_scrolling_window(&prompt, None);

        assert_eq!(visible_prompt[0], prompt[1]);
        assert_eq!(visible_prompt[1], prompt[2]);
    }
}
