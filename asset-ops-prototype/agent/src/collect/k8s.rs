use crate::model::{K8sNode, K8sPod, KubernetesInfo};
use crate::util::{
    command_exists, parse_cpu_cores, parse_quantity_bytes, run, truncate,
};
use serde_json::Value;
use std::path::PathBuf;
use std::time::Duration;

pub fn collect() -> KubernetesInfo {
    if !command_exists("kubectl") {
        return unavailable("未找到 kubectl");
    }
    let kubeconfig = kubeconfig_path();
    let context = kubectl(&kubeconfig, &["config", "current-context"], 5)
        .ok()
        .filter(|output| output.success)
        .map(|output| output.stdout.trim().to_string())
        .filter(|value| !value.is_empty());

    let nodes_output = match kubectl(
        &kubeconfig,
        &["get", "nodes", "-o", "json", "--request-timeout=5s"],
        15,
    ) {
        Ok(output) if output.success => output.stdout,
        Ok(output) => {
            return KubernetesInfo {
                available: false,
                context,
                version: None,
                error: Some(format!(
                    "kubectl get nodes 失败 ({}) {}",
                    output.code.unwrap_or(-1),
                    truncate(output.stderr.trim(), 240)
                )),
                ..empty_info()
            }
        }
        Err(error) => {
            return KubernetesInfo {
                available: false,
                context,
                version: None,
                error: Some(format!("kubectl 无法执行: {error}")),
                ..empty_info()
            }
        }
    };
    let nodes = parse_nodes(&nodes_output);
    let mut error = None;
    let pods = match kubectl(
        &kubeconfig,
        &["get", "pods", "-A", "-o", "json", "--request-timeout=5s"],
        20,
    ) {
        Ok(output) if output.success => parse_pods(&output.stdout),
        Ok(output) => {
            error = Some(format!(
                "kubectl get pods 失败 ({}) {}",
                output.code.unwrap_or(-1),
                truncate(output.stderr.trim(), 200)
            ));
            Vec::new()
        }
        Err(err) => {
            error = Some(format!("kubectl get pods 无法执行: {err}"));
            Vec::new()
        }
    };
    let mut pods_running = 0;
    let mut pods_pending = 0;
    let mut pods_failed = 0;
    let mut pods_succeeded = 0;
    let mut unhealthy = Vec::new();
    let pod_count = pods.len();
    for pod in &pods {
        match pod.phase.as_str() {
            "Running" => pods_running += 1,
            "Pending" => pods_pending += 1,
            "Failed" => pods_failed += 1,
            "Succeeded" => pods_succeeded += 1,
            _ => {}
        }
        if !matches!(pod.phase.as_str(), "Running" | "Succeeded") && unhealthy.len() < 200 {
            unhealthy.push(pod.clone());
        }
    }

    let namespace_count = kubectl(
        &kubeconfig,
        &["get", "namespaces", "-o", "json", "--request-timeout=5s"],
        10,
    )
    .ok()
    .filter(|output| output.success)
    .and_then(|output| serde_json::from_str::<Value>(output.stdout.trim()).ok())
    .and_then(|value| value.get("items").and_then(Value::as_array).map(Vec::len))
    .unwrap_or(0);

    let version = kubectl(&kubeconfig, &["version", "--client", "-o", "json"], 5)
        .ok()
        .filter(|output| output.success)
        .and_then(|output| serde_json::from_str::<Value>(output.stdout.trim()).ok())
        .and_then(|value| {
            value
                .get("clientVersion")
                .and_then(|v| v.get("gitVersion"))
                .and_then(Value::as_str)
                .map(|v| v.to_string())
        });

    KubernetesInfo {
        available: true,
        context,
        version,
        error,
        node_count: nodes.len(),
        nodes_ready: nodes.iter().filter(|node| node.ready).count(),
        nodes,
        namespace_count,
        pod_count,
        pods_running,
        pods_pending,
        pods_failed,
        pods_succeeded,
        unhealthy_pods: unhealthy,
    }
}

fn empty_info() -> KubernetesInfo {
    KubernetesInfo {
        available: false,
        context: None,
        version: None,
        error: None,
        node_count: 0,
        nodes_ready: 0,
        nodes: Vec::new(),
        namespace_count: 0,
        pod_count: 0,
        pods_running: 0,
        pods_pending: 0,
        pods_failed: 0,
        pods_succeeded: 0,
        unhealthy_pods: Vec::new(),
    }
}

fn unavailable(message: &str) -> KubernetesInfo {
    KubernetesInfo {
        error: Some(message.to_string()),
        ..empty_info()
    }
}

fn kubeconfig_path() -> Option<PathBuf> {
    if let Ok(value) = std::env::var("KUBECONFIG") {
        let first = value.split(':').next().unwrap_or("").trim();
        if !first.is_empty() {
            let path = PathBuf::from(first);
            if path.exists() {
                return Some(path);
            }
        }
    }
    let home = std::env::var("HOME").unwrap_or_default();
    let candidates = [
        format!("{home}/.kube/config"),
        "/etc/rancher/k3s/k3s.yaml".to_string(),
    ];
    candidates
        .into_iter()
        .map(PathBuf::from)
        .find(|path| path.exists())
}

fn kubectl(
    kubeconfig: &Option<PathBuf>,
    args: &[&str],
    timeout_secs: u64,
) -> anyhow::Result<crate::util::CommandOutput> {
    let mut full = Vec::new();
    if let Some(path) = kubeconfig {
        full.push("--kubeconfig".to_string());
        full.push(path.to_string_lossy().to_string());
    }
    full.extend(args.iter().map(|arg| arg.to_string()));
    let refs: Vec<&str> = full.iter().map(String::as_str).collect();
    run("kubectl", &refs, Duration::from_secs(timeout_secs))
}

fn parse_nodes(output: &str) -> Vec<K8sNode> {
    let Ok(value) = serde_json::from_str::<Value>(output.trim()) else {
        return Vec::new();
    };
    let mut nodes = Vec::new();
    if let Some(items) = value.get("items").and_then(Value::as_array) {
        for item in items.iter().take(1000) {
            if let Some(node) = parse_node(item) {
                nodes.push(node);
            }
        }
    }
    nodes
}

fn parse_node(item: &Value) -> Option<K8sNode> {
    let name = item
        .get("metadata")
        .and_then(|v| v.get("name"))
        .and_then(Value::as_str)?
        .to_string();
    let status = item.get("status")?;
    let ready = status
        .get("conditions")
        .and_then(Value::as_array)
        .map(|conditions| {
            conditions.iter().any(|condition| {
                condition.get("type").and_then(Value::as_str) == Some("Ready")
                    && condition.get("status").and_then(Value::as_str) == Some("True")
            })
        })
        .unwrap_or(false);
    let mut roles = Vec::new();
    if let Some(labels) = item
        .get("metadata")
        .and_then(|v| v.get("labels"))
        .and_then(Value::as_object)
    {
        for key in labels.keys() {
            if let Some(role) = key.strip_prefix("node-role.kubernetes.io/") {
                if !role.is_empty() {
                    roles.push(role.to_string());
                }
            }
        }
    }
    roles.sort();
    let node_info = status.get("nodeInfo");
    let capacity = status.get("capacity");
    Some(K8sNode {
        name,
        ready,
        roles,
        kubelet_version: node_info
            .and_then(|v| v.get("kubeletVersion"))
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string(),
        os: node_info
            .and_then(|v| v.get("operatingSystem"))
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string(),
        arch: node_info
            .and_then(|v| v.get("architecture"))
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string(),
        cpu_cores: capacity
            .and_then(|v| v.get("cpu"))
            .and_then(Value::as_str)
            .and_then(parse_cpu_cores),
        memory_bytes: capacity
            .and_then(|v| v.get("memory"))
            .and_then(Value::as_str)
            .and_then(parse_quantity_bytes),
    })
}

fn parse_pods(output: &str) -> Vec<K8sPod> {
    let Ok(value) = serde_json::from_str::<Value>(output.trim()) else {
        return Vec::new();
    };
    let mut pods = Vec::new();
    if let Some(items) = value.get("items").and_then(Value::as_array) {
        for item in items.iter().take(5000) {
            if let Some(pod) = parse_pod(item) {
                pods.push(pod);
            }
        }
    }
    pods
}

fn parse_pod(item: &Value) -> Option<K8sPod> {
    let metadata = item.get("metadata")?;
    let name = metadata.get("name").and_then(Value::as_str)?.to_string();
    let namespace = metadata
        .get("namespace")
        .and_then(Value::as_str)
        .unwrap_or("default")
        .to_string();
    let status = item.get("status");
    let phase = status
        .and_then(|v| v.get("phase"))
        .and_then(Value::as_str)
        .unwrap_or("Unknown")
        .to_string();
    let node = item
        .get("spec")
        .and_then(|v| v.get("nodeName"))
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_string();
    let mut restarts = 0_u64;
    let mut reason = String::new();
    if let Some(containers) = status
        .and_then(|v| v.get("containerStatuses"))
        .and_then(Value::as_array)
    {
        for container in containers {
            restarts += container
                .get("restartCount")
                .and_then(Value::as_u64)
                .unwrap_or(0);
            if reason.is_empty() {
                reason = container
                    .get("state")
                    .and_then(|v| v.get("waiting"))
                    .and_then(|v| v.get("reason"))
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .to_string();
            }
        }
    }
    if reason.is_empty() {
        reason = status
            .and_then(|v| v.get("reason"))
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_string();
    }
    Some(K8sPod {
        namespace,
        name,
        phase,
        node,
        restarts,
        reason: truncate(&reason, 120),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_node_roles_and_capacity() {
        let value: Value = serde_json::from_str(
            r#"{"metadata":{"name":"node-1","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"conditions":[{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.30.0","operatingSystem":"linux","architecture":"amd64"},"capacity":{"cpu":"8","memory":"32Gi"}}}"#,
        )
        .unwrap();
        let node = parse_node(&value).expect("node");
        assert!(node.ready);
        assert_eq!(node.roles, vec!["control-plane"]);
        assert_eq!(node.cpu_cores, Some(8.0));
        assert_eq!(node.memory_bytes, Some(32 * 1024 * 1024 * 1024));
    }

    #[test]
    fn parses_unhealthy_pod() {
        let value: Value = serde_json::from_str(
            r#"{"metadata":{"name":"api-1","namespace":"prod"},"spec":{"nodeName":"node-1"},"status":{"phase":"Pending","containerStatuses":[{"restartCount":2,"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}}"#,
        )
        .unwrap();
        let pod = parse_pod(&value).expect("pod");
        assert_eq!(pod.phase, "Pending");
        assert_eq!(pod.restarts, 2);
        assert_eq!(pod.reason, "ImagePullBackOff");
    }
}
