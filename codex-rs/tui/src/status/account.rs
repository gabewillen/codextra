#[derive(Debug, Clone)]
pub(crate) enum StatusAccountDisplay {
    ChatGpt {
        alias: String,
        email: Option<String>,
        plan: Option<String>,
    },
    ApiKey {
        alias: String,
    },
}
