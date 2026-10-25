use crate::model::{DockerContainer, DockerInfo};
use crate::util::{command_exists, parse_percent, run, truncate};
use serde_json::Value;
use std::collections::HashMap;
use std::time::Duration;

pub fn collect() -> DockerInfo {
    if !command_exists("docker") {
        return unavailable("未找到 docker 命令");
    }
    let info = run(
        "docker",
        &["info", "--format", "{{json .}}"],
        Duration::from_secs(10),
    );
    let info = match info {
        Ok(output) if output.success => match serde_json::from_str::<Value>(output.stdout.trim()) {
            Ok(value) => value,
            Err(error) => return unavailable(&format!("docker info 输出无法解析: {error}")),
        },
        Ok(output) => {
            return unavailable(&format!(
                "docker info 失败 ({}) {}",
                output.code.unwrap_or(-1),
                truncate(output.stderr.trim(), 200)
            ))
        }
        Err(error) => return unavailable(&format!("docker 无法执行: {error}")),
    };

    let mut error = None;
    let mut containers = match run(
        "docker",
        &["ps", "-a", "--no-trunc", "--format", "{{json .}}"],
        Duration::from_secs(15),
    ) {
        Ok(output) if output.success => output
            .stdout
            .lines()
            .filter_map(parse_container)
            .take(500)
            .collect::<Vec<_>>(),
        Ok(output) => {
            error = Some(format!(
                "docker ps 失败 ({}) {}",
                output.code.unwrap_or(-1),
                truncate(output.stderr.trim(), 200)
            ));
            Vec::new()
        }
        Err(err) => {
            error = Some(format!("docker ps 无法执行: {err}"));
            Vec::new()
        }
    };

    let running = containers.iter().filter(|c| c.state == "running").count();
    if running > 0 {
        apply_stats(&mut containers);
    }

    DockerInfo {
        available: true,
        version: info
            .get("ServerVersion")
            .and_then(Value::as_str)
            .map(|v| v.to_string()),
        error,
        images: info.get("Images").and_then(Value::as_u64),
        containers_total: info
            .get("Containers")
            .and_then(Value::as_u64)
            .map(|v| v as usize)
            .unwrap_or(containers.len()),
        containers_running: info
            .get("ContainersRunning")
            .and_then(Value::as_u64)
            .map(|v| v as usize)
            .unwrap_or(running),
        containers,
    }
}

fn unavailable(message: &str) -> DockerInfo {
    DockerInfo {
        available: false,
        version: None,
        error: Some(message.to_string()),
        images: None,
        containers_total: 0,
        containers_running: 0,
        containers: Vec::new(),
    }
}

fn parse_container(line: &str) -> Option<DockerContainer> {
    let value: Value = serde_json::from_str(line.trim()).ok()?;
    let get = |keys: &[&str]| -> String {
        for key in keys {
            if let Some(text) = value.get(*key).and_then(Value::as_str) {
                return text.to_string();
            }
        }
        String::new()
    };
    let id = get(&["ID", "Id"]);
    if id.is_empty() {
        return None;
    }
    Some(DockerContainer {
        id,
        name: get(&["Names", "Name"]),
        image: get(&["Image"]),
        state: get(&["State"]).to_lowercase(),
        status: get(&["Status"]),
        ports: get(&["Ports"]),
        created_at: get(&["CreatedAt", "Created"]),
        cpu_percent: None,
        memory_usage: None,
        memory_percent: None,
    })
}

fn apply_stats(containers: &mut [DockerContainer]) {
    let output = match run(
        "docker",
        &["stats", "--no-stream", "--format", "{{json .}}"],
        Duration::from_secs(20),
    ) {
        Ok(output) if output.success => output.stdout,
        Ok(output) => {
            tracing::debug!(
                status = output.code,
                stderr = %truncate(output.stderr.trim(), 200),
                "docker stats failed"
            );
            return;
        }
        Err(error) => {
            tracing::debug!(error = %error, "docker stats could not be executed");
            return;
        }
    };
    let mut stats: HashMap<String, (Option<f64>, Option<String>, Option<f64>)> = HashMap::new();
    for line in output.lines() {
        let Ok(value) = serde_json::from_str::<Value>(line.trim()) else {
            continue;
        };
        let id = value
            .get("ID")
            .or_else(|| value.get("Container"))
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string();
        if id.is_empty() {
            continue;
        }
        let cpu = value
            .get("CPUPerc")
            .and_then(Value::as_str)
            .and_then(parse_percent);
        let memory_usage = value
            .get("MemUsage")
            .and_then(Value::as_str)
            .map(|v| v.to_string());
        let memory_percent = value
            .get("MemPerc")
            .and_then(Value::as_str)
            .and_then(parse_percent);
        stats.insert(id, (cpu, memory_usage, memory_percent));
    }
    for container in containers {
        let key = if stats.contains_key(&container.id) {
            &container.id
        } else {
            &container.name
        };
        if let Some((cpu, memory_usage, memory_percent)) = stats.get(key) {
            container.cpu_percent = *cpu;
            container.memory_usage = memory_usage.clone();
            container.memory_percent = *memory_percent;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_docker_ps_json() {
        let line = r#"{"ID":"abc123","Names":"web","Image":"nginx:latest","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:8080->80/tcp","CreatedAt":"2026-10-25 10:00:00 +0800 CST"}"#;
        let container = parse_container(line).expect("container");
        assert_eq!(container.name, "web");
        assert_eq!(container.state, "running");
        assert_eq!(container.ports, "0.0.0.0:8080->80/tcp");
    }

    #[test]
    fn parses_docker_stats_percent() {
        assert_eq!(parse_percent("12.50%"), Some(12.5));
    }
}
