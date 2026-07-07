What ClusterIP does

ClusterIP is the default Kubernetes Service type — it gives a set of pods a single, stable virtual IP that only exists inside the cluster. In our case:

Service registry  →  ClusterIP 10.43.122.156:5000  →  registry pod (10.42.x.x:5000)

The problem it solves

Pods are ephemeral — the registry pod has an IP like 10.42.0.37, but if it restarts (crash, reschedule, redeploy) it gets a new IP. Anything that hardcoded the old pod IP breaks. ClusterIP is a fixed indirection: 10.43.122.156 stays constant for the life of the Service, and Kubernetes keeps a live list ("Endpoints") of which real pod IP(s) currently back it. Clients talk to the stable IP; K8s routes to whatever pod is healthy right now.

How it actually works (the mechanism)

1. The Service gets a virtual IP from the service CIDR (here 10.43.0.0/16 — that's why CoreDNS is 10.43.0.10 and our registry is 10.43.122.156). This IP is not assigned to any network interface — no machine "owns" it.
2. On every node, the CNI's datapath (Cilium here, via eBPF; classically kube-proxy via iptables) watches Services + Endpoints and installs rules: "packets to 10.43.122.156:5000 → DNAT to a real backing pod IP:port."
3. When a client sends a packet to the ClusterIP, that rule rewrites the destination to the actual pod on the wire. The ClusterIP never appears as a real network hop — it's purely a translation target.
4. DNS bonus: K8s also creates registry.default.svc.cluster.local → 10.43.122.156, so pods can use the name instead of the IP.

Why we chose it for the registry (the security reason)

This was Codex's High #1 finding. The three exposure options:

┌────────────────────┬────────────────────────────────────────────┬────────────────────────────────────────────────────┐
│        Type        │               Reachable from               │                        Risk                        │
├────────────────────┼────────────────────────────────────────────┼────────────────────────────────────────────────────┤
│ ClusterIP (chosen) │ inside the cluster only                    │ none externally — the IP isn't routable off-node   │
├────────────────────┼────────────────────────────────────────────┼────────────────────────────────────────────────────┤
│ NodePort           │ every node's IP, incl. public <build-box> │ exposes the unauth'd HTTP registry to the internet │
├────────────────────┼────────────────────────────────────────────┼────────────────────────────────────────────────────┤
│ hostNetwork :5000  │ all host interfaces, incl. public          │ same public-exposure risk                          │
└────────────────────┴────────────────────────────────────────────┴────────────────────────────────────────────────────┘

An unauthenticated HTTP registry is fine inside the cluster but must never be on the public node IP. ClusterIP gives zero host exposure — nothing binds to <build-box> — which is exactly why it's the default and our primary choice.

The nuance that matters for us (why Step 3.5 exists)

Normal pods reach a ClusterIP trivially. Our consumer is unusual: a SEV-SNP confidential guest — a QEMU VM whose traffic exits through the pod's veth with the pod IP as source, then Cilium applies the ClusterIP DNAT on the node. This should work identically to any pod (and the guest already reached external docker.io the same way), but "Cilium ClusterIP handling for Kata-VM-originated traffic" is the one thing we haven't directly confirmed — hence the Step-4 test: point the guest's mirror config at the registry and check the registry's access log for the pull. If for some reason the guest can't hit the ClusterIP, the documented fallback is binding the registry to the internal 10.0.0.216 only (+ firewall), never a public NodePort.

One practical consequence you'll see in the config: the initdata registries.conf currently uses the DNS name registry.default.svc.cluster.local:5000 (portable), but if the guest can't resolve cluster DNS I'll switch it to the raw ClusterIP 10.43.122.156:5000 (no DNS dependency) — both point at the same Service.