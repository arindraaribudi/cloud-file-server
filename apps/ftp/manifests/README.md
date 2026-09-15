# FTP Server Manifests

Apply in order:
1. `kubectl apply -f namespace.yaml`
2. Create the database Secret (template: `secret.yaml.example`):
   `kubectl apply -f ftp-db-secret.yaml -n ftp`
3. Configure CAM role-arn in `serviceaccount.yaml` and apply.
4. `kubectl apply -f configmap.yaml deployment.yaml service.yaml`
5. `kubectl create job -n ftp --from=cronjob/ftp-migrate ftp-migrate-now`
   (or `kubectl apply -f job-migrate.yaml` once)
6. `kubectl apply -f tcproute.yaml`

## TCPRoute caveat

`gateway.networking.k8s.io/v1alpha2` `TCPRoute` matches single ports, not ranges.
Three options, in order of preference:

(a) Use a Gateway implementation that supports a `TcpListener` with port ranges (e.g. Envoy Gateway).
(b) Front the service with a TCP multiplexer proxy (e.g. HAProxy `tproxy`).
(c) Accept the operational cost of 1000 `TCPRoute` objects.

Option (a) is recommended.

## Service.ftp-passive-50000 / 50001

The `Service` listed only ports 50000 and 50001 as a template. Production must list
all 50000-50999. Or use option (a) / (b) above.