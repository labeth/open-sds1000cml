# SDS1000CML+ engineering model

The documentation of the firmware: an engineering model (schema version 2), with
[`engmod.yml`](engmod.yml) as its manifest.

| File | Contents |
|---|---|
| `requirements.yml` | requirements (EARS), with stable `REQ-SDS-*` IDs cited from the code (`TRLC-LINKS`) |
| `decisions.yml` | design decisions (`ADR-*`): context, decision and consequences, including bench results |
| `architecture.yml` | functional units, hardware items and interfaces (`ENGMODEL-OWNER-UNIT` in the code) |
| `behavior.yml` | runtime flows |
| `views.yml` | architecture views |
| `catalog.yml`, `assurance.yml`, `compliance.yml` | supporting catalogue and assurance records (compliance is empty) |

Every source file names its unit (`ENGMODEL-OWNER-UNIT`) and every function the requirements it
serves (`TRLC-LINKS`). CI validates the model and those links across the repository with `engdoc` from
[engineering-model-go](https://github.com/labeth/engineering-model-go), at the release pinned in
`.github/workflows/ci.yml` (`ENGMODEL_VERSION`, v0.10.0):

```sh
engdoc --model model/engmod.yml --requirements model/requirements.yml \
  --design model/views.yml --code-root .. --out /tmp/ARCHITECTURE.adoc
```

The model is held to the life-cycle tier `product` with the foundation and security bundles
(`lifecycle` in `engmod.yml`, decision `DEC-TIER-PRODUCT`), and requirement links are strict: every
requirement realizes one of the capabilities published in `architecture.yml` (`CAP-SDS-*`) or is
derived with a rationale, and is at least approved. A product-tier finding that cannot be fixed yet
is carried as a POA&M item in `assurance.yml` with `milestone: tier:product` naming the diagnostic.
`engtrace` resolves `--code-root` from the working directory, so run it from `model/` to see what a
tier requires:

```sh
cd model && engtrace --model engmod.yml --requirements requirements.yml --code-root .. --tier-gap operational
```

Requirement statuses follow the evidence (`engws status-suggest --repo model`): implemented once the
code is linked, verified only with a passing RED check recorded for the tree.

Changes to behaviour go through the model first: record the decision in
`decisions.yml`, then change the code.
