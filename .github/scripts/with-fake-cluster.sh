#!/usr/bin/env bash
# Serves the fake API server and points KUBECONFIG at it while a command runs, so
# the GUI reads a cluster with one Ready node and two Running pods. The server
# and the kubeconfig are cleaned up when the command ends.
#
# usage: with-fake-cluster.sh <fake-apiserver binary> <command> [args...]
set -euo pipefail

bin=$1
shift
addr=127.0.0.1:7792

kubeconfig=$PWD/fake-kubeconfig
cat > "$kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: http://$addr
users:
- name: fake
  user: {}
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
current-context: fake
EOF

"$bin" -addr "$addr" > fake-apiserver.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT

for _ in $(seq 1 30); do
  curl -sf "http://$addr/api/v1/nodes" -o /dev/null && break
  sleep 1
done
curl -sf "http://$addr/api/v1/nodes" -o /dev/null || { cat fake-apiserver.log; exit 1; }

KUBECONFIG=$kubeconfig "$@"
