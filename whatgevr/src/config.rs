use std::time::Duration;

pub const APP_ID: &str = "in.codelif.Whatevr";

pub const ROOT_LOADING_PAGE: &str = "loading";
pub const ROOT_LOGIN_PAGE: &str = "login";
pub const ROOT_MAIN_PAGE: &str = "main";

pub const SIDEBAR_LOADING_PAGE: &str = "loading";
pub const SIDEBAR_EMPTY_PAGE: &str = "empty";
pub const SIDEBAR_LIST_PAGE: &str = "list";

pub const CONVERSATION_PLACEHOLDER_PAGE: &str = "placeholder";
pub const CONVERSATION_LOADING_PAGE: &str = "loading";
pub const CONVERSATION_EMPTY_PAGE: &str = "empty";
pub const CONVERSATION_MESSAGES_PAGE: &str = "messages";

pub const COMPOSER_MAX_HEIGHT: i32 = 144;
pub const COMPOSER_HEIGHT_SLACK: i32 = 2;

pub const RPC_TIMEOUT: Duration = Duration::from_secs(10);
pub const RPC_CONNECT_TIMEOUT: Duration = Duration::from_secs(3);
pub const TYPING_IDLE_TIMEOUT: Duration = Duration::from_secs(3);

pub const UI_MESSAGE_BATCH_SIZE: usize = 8;
