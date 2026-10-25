import json, sys
d = json.load(open(sys.argv[1]))
print("logs", [(x.get("path"), x.get("exists"), x.get("readable"), x.get("size_bytes")) for x in d.get("logs", [])])
print("metrics", bool(d["host"].get("metrics")), "history", len(d["users"].get("history", [])), "failed", len(d["users"].get("failed_logins", [])))