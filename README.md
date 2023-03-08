# Debug Me Maybe

You know how [ksniff](https://github.com/eldadru/ksniff) allows to remote
tcpdump a pod? Well this project attaches a debugger for Operators using `dlv`.
This is purely an adaptation of the ksniff source code.

## Build

```
make build install
```

## Usage

Attach to pid #1 on the `konnectivity-agent-p9ppv` pod in the `kube-system`
namespace and redirect the remote debugger port on your local machine
(2345/TCP by default):
```
kubectl dmm -n kube-system konnectivity-agent-p9ppv
```

Trying to Ctrl+C to shut it all off will only stop the port-forward. If you
want to also kill the remote debugger:
```
oc dmm -n kube-system konnectivity-agent-p9ppv --force-kill
```

## How?

1. Finds your pod
2. Uploads `dlv` (https://github.com/go-delve/delve, a go debugger) onto the
   pod (in /tmp/dlv by default)
3. Attaches the debugger for a pid on your pod and listens for debug commands
   on a port (2345/tcp by default)
4. Locally spawns a port-forward (kubectl port-forward) to expose the remote
   debugger port onto your local machine
