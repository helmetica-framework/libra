# Libra

**Libra**: the alchemist's scales, used to weigh each ingredient before the work begins.

A `BillingData` (`libra.helmetica.io/v1`) is libra's resource. For now the
controller only sets its status to `Ready`.

## Quickstart

Against a kind (or any) cluster:

```bash
kubectl apply -k config/crd
just run          # in a second terminal
kubectl apply -k config/samples
kubectl get billingdata -w   # PHASE -> Ready
```

Set `spec.suspend: true` to pause it. A suspended object holds at `Pending`.

**`config/default` deploys RBAC and the manager, but not the CRDs.** Install
the CRDs separately from `config/crd` first:

```bash
kubectl apply -k config/crd
kubectl apply -k config/default
```

## Structure

| Path | What's there |
| ---- | ------------- |
| `api/v1` | The `BillingData` type: spec, status and generated deepcopy code. |
| `cmd` | The `libra` CLI (cobra): `controller.go` builds the manager. |
| `controllers` | `BillingDataManager`. |
| `config` | Kustomize tree: `crd` (installed separately), `rbac`, `manager`, `default` (RBAC + manager, no CRDs) and `samples`. |
