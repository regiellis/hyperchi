---
title: Network layout and address plan
tags: network, vlan, dns
---

# Network layout and address plan

Everything sits behind the OPNsense box. The switch trunks four VLANs to it.

| VLAN | Name | Subnet | Notes |
| --- | --- | --- | --- |
| 10 | trusted | 10.10.10.0/24 | Laptops and phones |
| 20 | servers | 10.10.20.0/24 | Proxmox hosts, NAS, the Docker host `atlas` |
| 30 | iot | 10.10.30.0/24 | No route to other VLANs; internet only |
| 40 | guest | 10.10.40.0/24 | Client isolation on the access points |

## Fixed addresses

- `10.10.20.2` opnsense (gateway for every VLAN at `.1` is the same box)
- `10.10.20.10` nas
- `10.10.20.21` atlas (Docker host, reverse proxy)
- `10.10.20.30` pve1, `10.10.20.31` pve2

## DNS

Unbound on OPNsense answers for `lab.home.arpa`. Host overrides live in
`configs/dns/unbound-local.conf`. Split DNS sends `*.lab.home.arpa` to the
proxy on atlas, which terminates TLS with the wildcard certificate.
