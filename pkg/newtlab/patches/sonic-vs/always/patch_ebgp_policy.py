import sys
path = '/usr/local/sonic/frrcfgd/bgpd.conf.db.j2'
with open(path) as f:
    t = f.read()
if 'ebgp_requires_policy' in t:
    print('already patched')
    sys.exit(0)
anchor = " no bgp default ipv4-unicast\n{% endif %}"
patch = anchor + "\n{% if 'ebgp_requires_policy' in bgp_sess and bgp_sess['ebgp_requires_policy'] == 'false' %}\n no bgp ebgp-requires-policy\n{% endif %}"
t2 = t.replace(anchor, patch, 1)
if t2 == t:
    print('anchor not found')
    sys.exit(1)
with open(path, 'w') as f:
    f.write(t2)
print('patched')
