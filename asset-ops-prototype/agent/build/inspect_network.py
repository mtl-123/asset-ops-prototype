import json, sys
d = json.load(open(sys.argv[1]))
n = d["host"]["network"]
print("selection", n.get("selection"), "selected", n.get("selected_interfaces"), "interval", n.get("sample_interval_seconds"), "error", n.get("error"))
for item in n.get("interfaces", []):
    print(item.get("name"), "rx", item.get("received_bytes"), "tx", item.get("transmitted_bytes"), "rx_bps", item.get("received_bps"), "tx_bps", item.get("transmitted_bps"), "err/drop", item.get("receive_errors"), item.get("receive_dropped"))