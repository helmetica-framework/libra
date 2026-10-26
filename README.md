# Libra

**Libra**: the alchemist's scales, used to weigh each ingredient before the
work begins.

Libra collects what the services of the [helmetica](https://github.com/helmetica-framework)
framework are billed for. A service declares its billable amounts in a
`BillingData` next to it, and libra turns each one into a billing record tied
to the claim that ordered the service.

Libra only collects. Prices, invoices and the ERP are somebody else's job.

## Quickstart

Against a kind (or any) cluster:

```bash
kubectl apply -k config/crd
just run          # in a second terminal
kubectl annotate namespace default \
  chrysopoeia.io/claim-apiVersion=example.io/v1 \
  chrysopoeia.io/claim-kind=Redis \
  chrysopoeia.io/claim-namespace=tenant \
  chrysopoeia.io/claim-name=my-redis \
  chrysopoeia.io/claim-uid=example-uid
kubectl apply -k config/samples
kubectl get billingdata -w   # PHASE -> Ready
```

The annotations stand in for chrysopoeia, which sets them on every instance
namespace. The sample creates a `BillingData` named `sample` with two items,
and the controller sets its phase to `Ready` with the claim it found:

```console
NAME     CLAIM      PHASE   MESSAGE
sample   my-redis   Ready   billing data ready
```

**`config/default` deploys RBAC and the manager, but not the CRDs.** Install
the CRDs separately from `config/crd` first:

```bash
kubectl apply -k config/crd
kubectl apply -k config/default
```

## Declaring what is billed

```yaml
apiVersion: libra.helmetica.io/v1
kind: BillingData
metadata:
  name: sample
spec:
  metadata:
    sla: besteffort
  items:
    - name: replicas
      value: "1"
    - name: storage
      value: 10Gi
```

An empty spec is valid: it builds a record with no items.

| Field | Default | Meaning |
| ----- | ------- | ------- |
| `metadata` | none | Free text that applies to every item, such as the SLA. |
| `items` | none | At most 64 entries. |
| `items[].name` | required | What is billed. Each name may appear once. |
| `items[].value` | required | How much, as a Kubernetes quantity: `3`, `0.5` or `10Gi`. Anything else is rejected when it is applied, so a typo fails at `helm install`. |
| `suspend` | `false` | Holds the object at `Pending` and builds no record. Effectively disabling billing for this `BillingData`. |

The result is reported back on the object:

| Status field | Meaning |
| ------------ | ------- |
| `phase` | `Ready`, `Pending` while suspended, `Failed` when the claim is unknown. |
| `message` | What happened. For `Failed`, which annotations are missing. |
| `claimNamespace`, `claimName` | The claim the record is billed to. |

## Where the BillingData comes from

The service chart renders it from its own values, so the chart author decides
what is billable and the values are final before they reach the cluster.
Making them easier to trace and debug.

## Where the claim comes from

**Libra never reads the claim.** Chrysopoeia provides annotations to every instance namespace
with the claim it belongs to, and libra reads it from there:

`chrysopoeia.io/claim-apiVersion`, `claim-kind`, `claim-namespace`,
`claim-name`, `claim-uid`.

If one is missing or empty, the `BillingData` goes `Failed` and the message
names the missing ones. Libra watches namespaces, so it turns `Ready` on its
own once the annotations appear.

## Design notes

**Values are passed on exactly as written.** Libra checks that a value is a
quantity but never converts it, so `1.5Gi` stays `1.5Gi` and does not become
`1536Mi`. Unit conversion belongs to whatever reads the records.

## Structure

| Path | What's there |
| ---- | ------------- |
| `api/v1` | The `BillingData` type (`libra.helmetica.io/v1`): spec, status, the quantity check and generated deepcopy code. |
| `cmd` | The `libra` CLI (cobra): `root.go` wires the binary, `controller.go` is the `libra controller` subcommand that builds the manager and registers `BillingDataManager`. |
| `controllers` | `BillingDataManager`, the reconciler for `BillingData`. `desiredState` decides each pass; `claim.go` reads the namespace annotations and `record.go` builds the record. |
| `config` | Kustomize tree: `crd` (the CRD manifests, installed separately), `rbac`, `manager`, `default` (RBAC + manager, no CRDs) and `samples`. |
