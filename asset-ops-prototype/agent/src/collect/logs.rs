use crate::config::LogsConfig;
use crate::model::LogFileStatus;
use chrono::{DateTime, SecondsFormat, Utc};
use std::collections::HashSet;
use std::fs;
use std::path::Path;

pub fn collect(config: &LogsConfig) -> Vec<LogFileStatus> {
    if !config.enabled {
        return Vec::new();
    }
    let mut seen = HashSet::new();
    let mut rows = Vec::new();
    for path in config.paths.iter().chain(config.business_paths.iter()) {
        let path = path.trim();
        if path.is_empty() || !seen.insert(path.to_string()) {
            continue;
        }
        rows.push(status(path));
    }
    rows.sort_by(|left, right| left.path.cmp(&right.path));
    rows
}

fn status(path: &str) -> LogFileStatus {
    let file = Path::new(path);
    if !file.exists() {
        return LogFileStatus {
            path: path.to_string(),
            exists: false,
            readable: false,
            size_bytes: 0,
            modified_at: None,
            error: Some("文件不存在".to_string()),
        };
    }
    let metadata = match fs::metadata(file) {
        Ok(value) => value,
        Err(error) => {
            return LogFileStatus {
                path: path.to_string(),
                exists: true,
                readable: false,
                size_bytes: 0,
                modified_at: None,
                error: Some(format!("读取元数据失败: {error}")),
            }
        }
    };
    let readable = fs::File::open(file).is_ok();
    let modified = metadata
        .modified()
        .ok()
        .map(|value| DateTime::<Utc>::from(value).to_rfc3339_opts(SecondsFormat::Secs, true));
    LogFileStatus {
        path: path.to_string(),
        exists: true,
        readable,
        size_bytes: metadata.len(),
        modified_at: modified,
        error: if readable {
            None
        } else {
            Some("文件不可读（检查权限）".to_string())
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn disabled_logs_are_not_scanned() {
        let config = LogsConfig::default();
        assert!(collect(&config).is_empty());
    }

    #[test]
    fn missing_path_is_reported() {
        let mut config = LogsConfig::default();
        config.enabled = true;
        config.paths = vec!["/tmp/qiyun-no-such-log-file".to_string()];
        config.business_paths.clear();
        let rows = collect(&config);
        assert_eq!(rows.len(), 1);
        assert!(!rows[0].exists);
        assert!(!rows[0].readable);
    }
}
