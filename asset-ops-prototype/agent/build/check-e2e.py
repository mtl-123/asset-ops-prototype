import json
hosts = json.load(open('/tmp/qiyun-hosts.json'))
items = hosts.get('items', [])
assert items, 'no agent hosts returned'
first = items[0]
print('agent_hosts', len(items))
print('first', first['host_id'], first['hostname'], first['cpu_percent'], first['docker_running'])
open('/tmp/qiyun-host-id', 'w').write(first['host_id'])
detail = json.load(open('/tmp/qiyun-detail.json'))
payload = detail['payload']
assert payload['host']['hostname'] == first['hostname']
print('detail_host', payload['host']['hostname'], 'disks', len(payload['host']['disks']))
print('detail_users', payload['users']['logged_in_count'], payload['users']['stale_count'])
print('detail_platforms', payload['platforms']['pve']['available'], payload['platforms']['docker']['available'], payload['platforms']['kubernetes']['available'])
assert isinstance(payload['platforms']['docker']['containers'], list)