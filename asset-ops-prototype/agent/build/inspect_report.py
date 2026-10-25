import json, sys
d = json.load(open(sys.argv[1]))
print("keys", sorted(d))
print("host", d["host"]["hostname"], d["host"]["cpu"]["logical_cores"], d["host"]["memory"]["usage_percent"])
print("users", d["users"]["logged_in_count"], d["users"]["stale_count"], d["users"]["lastlog_available"])
print("pve", d["platforms"]["pve"]["available"])
print("docker", d["platforms"]["docker"]["available"], d["platforms"]["docker"]["containers_total"])
print("k8s", d["platforms"]["kubernetes"]["available"])