import json, sys
d = json.load(open(sys.argv[1]))
h = d["host"]
print("host", h["hostname"], "network", h["network"].get("selected_interfaces"))
m = h.get("metrics")
print("metrics_interval", m.get("sample_interval_seconds") if m else None)
if m:
    print("cpu", m.get("cpu"))
    print("memory", m.get("memory"))
    print("swap", m.get("swap"))
    print("disks", [(i.get("device"), i.get("read_iops"), i.get("util_percent")) for i in m.get("disks", [])])
    print("inodes", len(m.get("inodes", [])), "tcp", m.get("tcp"), "process", m.get("processes"))
u = d["users"]
print("history", len(u.get("history", [])), u.get("history_available"), u.get("history_error"))
print("failed", len(u.get("failed_logins", [])), u.get("failed_available"), u.get("failed_error"))