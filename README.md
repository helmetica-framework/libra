# Libra

**Libra**: the alchemist's scales, used to weigh each ingredient before the
work begins.

Libra collects what the services of the [helmetica](https://github.com/helmetica-framework)
framework are billed for. A service declares its billable amounts in a
`BillingData` next to it, and libra turns each one into a billing record tied
to the claim that ordered the service. Libra writes the records to an S3
bucket.

Libra only collects. Prices, invoices and the ERP are somebody else's job.

## Quickstart

Against a kind (or any) cluster, with a local minio for the bucket:

```bash
kubectl apply -k config/crd
just run
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

The record is now in the bucket:

```bash
docker exec minio mc ls --recursive local/billing
```

**`config/default` deploys RBAC and the manager, but not the CRDs.** Install
the CRDs separately from `config/crd` first:

```bash
kubectl apply -k config/crd
kubectl apply -k config/default
```

The manager reads its S3 settings from the secret `libra-s3`. Create it before
the manager starts:

```bash
kubectl create secret generic libra-s3 -n <libra namespace> \
  --from-literal=S3_ENDPOINT=https://s3.example.com \
  --from-literal=S3_BUCKET=billing \
  --from-literal=AWS_ACCESS_KEY_ID=... \
  --from-literal=AWS_SECRET_ACCESS_KEY=...
```

## Settings

| Flag | Env var | Meaning |
| ---- | ------- | ------- |
| `--s3-endpoint` | `S3_ENDPOINT` | S3 server URL with `http://` or `https://`. Required. |
| `--s3-bucket` | `S3_BUCKET` | Bucket for the records. Required. It must exist, or libra does not start. |
| none | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | S3 credentials. |

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
| `phase` | `Ready`, `Pending` while suspended, `Failed` when the claim is unknown or the S3 write failed. |
| `message` | What happened. For `Failed`, which annotations are missing or the S3 error. |
| `claimNamespace`, `claimName` | The claim the record is billed to. |
| `pendingReport` | The record libra is about to write. A retry uses the same key. |
| `lastReport` | The last record written to S3. |

## Records in S3

Libra writes one JSON object for each new generation or new claim. A pass with
no change writes nothing. The first object of a `BillingData` is `created`,
later ones are `observed`.

| Event | Key |
| ----- | --- |
| `created`, `observed` | `YYYY/MM/DD/THHMMSSZ_<uid>_<generation>.json` |
| `deleted` | `YYYY/MM/DD/THHMMSSZ_<uid>_deleted.json` |

Times are UTC, so keys sort by time. The body:

```json
{
  "version": 1,
  "event": "observed",
  "observedAt": "2026-10-26T12:00:00Z",
  "claim": {"apiVersion": "example.io/v1", "kind": "Redis", "namespace": "tenant", "name": "my-redis", "uid": "claim-uid"},
  "source": {"namespace": "instance", "name": "sample", "uid": "bd-uid", "generation": 4},
  "metadata": {"sla": "guaranteed"},
  "items": [{"name": "replicas", "value": "3"}]
}
```

Libra puts the finalizer `libra.helmetica.io/report-deletion` on every
`BillingData`. On delete it writes the `deleted` record, then removes the
finalizer. If nothing was reported before, it writes nothing.

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

## Uninstall

Remove the service charts before libra. If libra goes first, their
`BillingData` objects keep the finalizer and never go away. To fix that by
hand:

```bash
kubectl patch billingdata <name> -n <namespace> --type=merge -p '{"metadata":{"finalizers":null}}'
```

## Structure

| Path | What's there |
| ---- | ------------- |
| `api/v1` | The `BillingData` type (`libra.helmetica.io/v1`): spec, status, the quantity check and generated deepcopy code. |
| `cmd` | The `libra` CLI (cobra): `root.go` wires the binary, `controller.go` is the `libra controller` subcommand that builds the manager and registers `BillingDataManager`. |
| `controllers` | `BillingDataManager`, the reconciler for `BillingData`. `desiredState` decides each pass; `claim.go` reads the namespace annotations, `record.go` builds the record and `report.go` picks the S3 key and body. |
| `s3` | The minio-based writer that puts records into the bucket. |
| `config` | Kustomize tree: `crd` (the CRD manifests, installed separately), `rbac`, `manager`, `default` (RBAC + manager, no CRDs) and `samples`. |
