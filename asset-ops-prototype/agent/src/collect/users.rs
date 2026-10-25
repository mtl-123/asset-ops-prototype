use crate::config::AuditConfig;
use crate::model::{FailedLogin, LoginRecord, LoginSession, StaleUser, UsersInfo};
use crate::util::{command_exists, run};
use chrono::{DateTime, Utc};
use std::collections::HashSet;
use std::time::Duration;

pub fn collect(stale_days: i64, audit: &AuditConfig) -> UsersInfo {
    let local_users = local_users();
    let (logged_in, session_count) = who_sessions();
    let mut logged_users = HashSet::new();
    for session in &logged_in {
        logged_users.insert(session.user.clone());
    }

    let (stale, never_logged_in, lastlog_available) = if audit.lastlog {
        let lastlog_command = if command_exists("lastlog") {
            Some("lastlog")
        } else if command_exists("lastlog2") {
            Some("lastlog2")
        } else {
            None
        };
        match lastlog_command {
            Some(command) => match run(command, &[], Duration::from_secs(10)) {
                Ok(output) if output.success => {
                    let (stale, never) = parse_lastlog(&output.stdout, &local_users, stale_days);
                    (stale, never, true)
                }
                Ok(output) => {
                    tracing::debug!(
                        command,
                        status = output.code,
                        stderr = %output.stderr.trim(),
                        "lastlog/lastlog2 is not available"
                    );
                    (Vec::new(), Vec::new(), false)
                }
                Err(error) => {
                    tracing::debug!(command, error = %error, "lastlog/lastlog2 could not be executed");
                    (Vec::new(), Vec::new(), false)
                }
            },
            None => {
                tracing::debug!("neither lastlog nor lastlog2 is installed");
                (Vec::new(), Vec::new(), false)
            }
        }
    } else {
        (Vec::new(), Vec::new(), false)
    };

    let (history, history_available, history_error) = if audit.enabled {
        login_history(audit.history_limit)
    } else {
        (Vec::new(), false, None)
    };
    let (failed_logins, failed_available, failed_error) = if audit.enabled {
        failed_history(audit.failed_limit)
    } else {
        (Vec::new(), false, None)
    };

    UsersInfo {
        logged_in_count: logged_users.len(),
        session_count,
        logged_in,
        local_user_count: local_users.len(),
        stale_days,
        stale_count: stale.len() + never_logged_in.len(),
        stale,
        never_logged_in,
        lastlog_available,
        history,
        failed_logins,
        history_available,
        failed_available,
        history_error,
        failed_error,
    }
}

fn login_history(limit: usize) -> (Vec<LoginRecord>, bool, Option<String>) {
    let limit = limit.clamp(1, 5000).to_string();
    match run("last", &["-F", "-w", "-n", limit.as_str()], Duration::from_secs(15)) {
        Ok(output) if output.success => {
            let records = output
                .stdout
                .lines()
                .filter_map(parse_last_record)
                .take(limit.parse().unwrap_or(200))
                .collect();
            (records, true, None)
        }
        Ok(output) => (
            Vec::new(),
            false,
            Some(format!(
                "last 失败 ({}) {}",
                output.code.unwrap_or(-1),
                output.stderr.trim()
            )),
        ),
        Err(error) => (Vec::new(), false, Some(format!("last 无法执行: {error}"))),
    }
}

fn failed_history(limit: usize) -> (Vec<FailedLogin>, bool, Option<String>) {
    let limit = limit.clamp(1, 5000).to_string();
    let limit_number = limit.parse().unwrap_or(200);
    let lastb_error = match run("lastb", &["-F", "-w", "-n", limit.as_str()], Duration::from_secs(15)) {
        Ok(output) if output.success => {
            let records = output
                .stdout
                .lines()
                .filter_map(parse_failed_record)
                .take(limit_number)
                .collect();
            return (records, true, None);
        }
        Ok(output) => format!(
            "lastb 失败 ({}) {}",
            output.code.unwrap_or(-1),
            output.stderr.trim()
        ),
        Err(error) => format!("lastb 无法执行: {error}"),
    };

    // Ubuntu 26.04 / Debian 13 no longer ship lastb. lslogins reads btmp
    // and exposes the same failed-login data, so use it as a real fallback.
    if command_exists("lslogins") {
        match run(
            "lslogins",
            &[
                "--failed",
                "-o",
                "USER,FAILED-LOGIN,FAILED-TTY",
                "--noheadings",
                "--notruncate",
                "--time-format=iso",
            ],
            Duration::from_secs(15),
        ) {
            Ok(output) if output.success => {
                let records = output
                    .stdout
                    .lines()
                    .filter_map(parse_lslogins_failed)
                    .take(limit_number)
                    .collect();
                return (records, true, None);
            }
            Ok(output) => {
                return (
                    Vec::new(),
                    false,
                    Some(format!(
                        "{lastb_error}；lslogins --failed 也失败 ({}) {}",
                        output.code.unwrap_or(-1),
                        output.stderr.trim()
                    )),
                )
            }
            Err(error) => {
                return (
                    Vec::new(),
                    false,
                    Some(format!("{lastb_error}；lslogins 无法执行: {error}")),
                )
            }
        }
    }
    (
        Vec::new(),
        false,
        Some(format!(
            "{lastb_error}；未找到 lastb 或 lslogins（可安装 util-linux-extra）"
        )),
    )
}

fn parse_lslogins_failed(line: &str) -> Option<FailedLogin> {
    let fields: Vec<&str> = line.split_whitespace().collect();
    if fields.len() < 2 {
        return None;
    }
    let user = fields[0];
    let time = fields[1];
    if user.is_empty()
        || time.is_empty()
        || time == "-"
        || time.eq_ignore_ascii_case("never")
        || time == "**Never"
    {
        return None;
    }
    Some(FailedLogin {
        user: user.to_string(),
        tty: fields.get(2).unwrap_or(&"").to_string(),
        host: String::new(),
        time: time.to_string(),
        raw: line.trim().to_string(),
    })
}

fn parse_last_record(line: &str) -> Option<LoginRecord> {
    let trimmed = line.trim();
    if trimmed.is_empty() || trimmed.contains("wtmp begins") || trimmed.contains("btmp begins") {
        return None;
    }
    let tokens: Vec<&str> = trimmed.split_whitespace().collect();
    if tokens.len() < 6 {
        return None;
    }
    let start = tokens.iter().position(|token| is_weekday(token))?;
    if tokens.len() < start + 5 {
        return None;
    }
    let started = tokens[start..start + 5].join(" ");
    let tail = &tokens[start + 5..];
    let joined = tail.join(" ");
    let (ended, duration) = if joined.contains("still logged in") {
        ("still logged in".to_string(), String::new())
    } else if joined.contains("still running") {
        ("still running".to_string(), String::new())
    } else if let Some(index) = tail.iter().position(|token| *token == "-") {
        let end_date = tail
            .get(index + 1..index + 6)
            .map(|parts| parts.join(" "))
            .unwrap_or_default();
        let duration = tail
            .iter()
            .find(|token| token.starts_with('(') && token.ends_with(')'))
            .map(|token| token.trim_matches(|c| c == '(' || c == ')').to_string())
            .unwrap_or_default();
        (end_date, duration)
    } else {
        (String::new(), String::new())
    };
    let user = tokens[0];
    Some(LoginRecord {
        user: user.to_string(),
        tty: tokens[1].to_string(),
        host: tokens[2].to_string(),
        started,
        ended,
        duration,
        kind: if user.eq_ignore_ascii_case("reboot") {
            "reboot".to_string()
        } else {
            "login".to_string()
        },
        raw: trimmed.to_string(),
    })
}

fn parse_failed_record(line: &str) -> Option<FailedLogin> {
    let trimmed = line.trim();
    if trimmed.is_empty() || trimmed.contains("btmp begins") {
        return None;
    }
    let tokens: Vec<&str> = trimmed.split_whitespace().collect();
    if tokens.len() < 6 {
        return None;
    }
    let start = tokens.iter().position(|token| is_weekday(token))?;
    if tokens.len() < start + 5 {
        return None;
    }
    Some(FailedLogin {
        user: tokens[0].to_string(),
        tty: tokens[1].to_string(),
        host: tokens[2].to_string(),
        time: tokens[start..start + 5].join(" "),
        raw: trimmed.to_string(),
    })
}

fn local_users() -> HashSet<String> {
    let content = std::fs::read_to_string("/etc/passwd").unwrap_or_default();
    let mut users = HashSet::new();
    for line in content.lines() {
        let fields: Vec<&str> = line.split(':').collect();
        if fields.len() < 7 {
            continue;
        }
        let name = fields[0].trim();
        let Ok(uid) = fields[2].parse::<u32>() else {
            continue;
        };
        let shell = fields[6].trim();
        if uid < 1000 || uid >= 60000 || name.is_empty() {
            continue;
        }
        if shell.ends_with("nologin") || shell.ends_with("false") {
            continue;
        }
        users.insert(name.to_string());
    }
    users
}

fn who_sessions() -> (Vec<LoginSession>, usize) {
    let mut sessions = Vec::new();
    match run("who", &[], Duration::from_secs(5)) {
        Ok(output) if output.success => {
            for line in output.stdout.lines() {
                if let Some(session) = parse_who_line(line) {
                    sessions.push(session);
                }
            }
        }
        Ok(output) => tracing::debug!(status = output.code, "who failed"),
        Err(error) => tracing::debug!(error = %error, "who could not be executed"),
    }
    let count = sessions.len();
    (sessions, count)
}

fn parse_who_line(line: &str) -> Option<LoginSession> {
    let fields: Vec<&str> = line.split_whitespace().collect();
    if fields.len() < 4 {
        return None;
    }
    let user = fields[0];
    if user.eq_ignore_ascii_case("LOGIN") || user.starts_with(' ') {
        return None;
    }
    let tty = fields[1];
    let date = fields[2];
    let time = fields[3];
    let host = fields
        .get(4)
        .map(|v| v.trim_matches(|c| c == '(' || c == ')').to_string())
        .unwrap_or_default();
    Some(LoginSession {
        user: user.to_string(),
        tty: tty.to_string(),
        host,
        login_time: format!("{date} {time}"),
    })
}

fn parse_lastlog(
    output: &str,
    local_users: &HashSet<String>,
    stale_days: i64,
) -> (Vec<StaleUser>, Vec<String>) {
    let mut stale = Vec::new();
    let mut never = Vec::new();
    for line in output.lines() {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.is_empty() || fields[0].eq_ignore_ascii_case("Username") {
            continue;
        }
        let user = fields[0];
        if user.starts_with("**") || (!local_users.is_empty() && !local_users.contains(user)) {
            continue;
        }
        if line.contains("Never logged in") {
            never.push(user.to_string());
            continue;
        }
        let Some(raw_date) = lastlog_date(&fields, line) else {
            continue;
        };
        let Some(parsed) = parse_lastlog_date(&raw_date) else {
            continue;
        };
        let days = (Utc::now() - parsed).num_days();
        if days > stale_days {
            stale.push(StaleUser {
                user: user.to_string(),
                last_login: Some(parsed.to_rfc3339()),
                days_inactive: Some(days),
                never: false,
            });
        }
    }
    stale.sort_by(|a, b| b.days_inactive.cmp(&a.days_inactive));
    never.sort();
    (stale, never)
}

fn lastlog_date(fields: &[&str], _line: &str) -> Option<String> {
    for (index, field) in fields.iter().enumerate() {
        if is_weekday(field) || is_month(field) {
            return Some(fields[index..].join(" "));
        }
    }
    None
}

fn parse_lastlog_date(value: &str) -> Option<DateTime<Utc>> {
    let value = value.trim();
    for format in [
        "%a %b %e %H:%M:%S %z %Y",
        "%a %b %d %H:%M:%S %z %Y",
        "%b %e %H:%M:%S %z %Y",
        "%Y-%m-%d %H:%M:%S %z",
    ] {
        if let Ok(parsed) = DateTime::parse_from_str(value, format) {
            return Some(parsed.with_timezone(&Utc));
        }
    }
    None
}

fn is_weekday(value: &str) -> bool {
    matches!(value, "Mon" | "Tue" | "Wed" | "Thu" | "Fri" | "Sat" | "Sun")
}

fn is_month(value: &str) -> bool {
    matches!(
        value,
        "Jan" | "Feb" | "Mar" | "Apr" | "May" | "Jun" | "Jul" | "Aug" | "Sep" | "Oct" | "Nov" | "Dec"
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_who_sessions() {
        let session = parse_who_line("meitaolin pts/0        2026-10-25 21:00 (10.0.0.5)")
            .expect("session");
        assert_eq!(session.user, "meitaolin");
        assert_eq!(session.tty, "pts/0");
        assert_eq!(session.host, "10.0.0.5");
        assert_eq!(session.login_time, "2026-10-25 21:00");
    }

    #[test]
    fn parses_lastlog_never_and_stale() {
        let output = "Username         Port     From             Latest\n\
root                                       **Never logged in**\n\
alice            pts/1    10.0.0.9         Mon Jan  1 10:00:00 +0800 2024\n\
bob              pts/2    10.0.0.8         **Never logged in**\n\
recent           pts/3    10.0.0.7         Sun Oct 25 21:00:00 +0800 2026\n";
        let local: HashSet<String> = ["alice", "bob", "recent"]
            .iter()
            .map(|v| v.to_string())
            .collect();
        let (stale, never) = parse_lastlog(output, &local, 90);
        assert_eq!(never, vec!["bob".to_string()]);
        assert_eq!(stale.len(), 1);
        assert_eq!(stale[0].user, "alice");
        assert!(stale[0].days_inactive.unwrap_or(0) > 90);
    }

    #[test]
    fn parses_wtmp_login_records() {
        let still = parse_last_record(
            "alice    pts/0    10.0.0.5    Sun Oct 25 21:00:00 2026   still logged in",
        )
        .expect("still");
        assert_eq!(still.user, "alice");
        assert_eq!(still.started, "Sun Oct 25 21:00:00 2026");
        assert!(still.ended.contains("still"));
        let closed = parse_last_record(
            "bob      pts/1    10.0.0.6    Sun Oct 25 18:00:00 2026 - Sun Oct 25 20:00:00 2026  (02:00)",
        )
        .expect("closed");
        assert_eq!(closed.duration, "02:00");
        assert!(closed.ended.contains("Sun Oct 25 20:00:00 2026"));
        let reboot = parse_last_record(
            "reboot   system boot  6.8.12-1-pve Sun Oct 25 08:00:00 2026   still running",
        )
        .expect("reboot");
        assert_eq!(reboot.kind, "reboot");
    }

    #[test]
    fn parses_lslogins_failed_rows() {
        let record = parse_lslogins_failed("root 2026-10-25T21:00:00+0800 ssh:notty")
            .expect("lslogins failed row");
        assert_eq!(record.user, "root");
        assert!(record.time.contains("2026"));
        assert_eq!(record.tty, "ssh:notty");
        assert!(parse_lslogins_failed("root").is_none());
    }

    #[test]
    fn parses_btmp_failed_records() {
        let record = parse_failed_record(
            "root     ssh:notty  10.0.0.9    Sun Oct 25 21:00:00 2026",
        )
        .expect("failed");
        assert_eq!(record.user, "root");
        assert_eq!(record.host, "10.0.0.9");
        assert!(record.time.contains("2026"));
    }
}
