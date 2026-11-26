# Libra

**Libra**: the alchemist's scales, used to weigh each ingredient before the work begins.

Libra collects what the services of the helmetica framework are billed for: each `BillingData` becomes a billing record tied to the claim that ordered the service, written to an S3 bucket. Prices, invoices and the ERP are somebody else's job.

Documentation: https://docs.helmetica.io/libra/

## Development

```bash
just test
just run    # against the cluster in your KUBECONFIG
just docs   # preview the docs
```
