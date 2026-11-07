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
cd model
engdoc --model engmod.yml --requirements requirements.yml \
  --design views.yml --code-root .. --lock check --out /tmp/ARCHITECTURE.adoc
```

The model composes open-dso-core's model (`engmod.yml` dependency `core`, subsystem `SUB-CORE`,
decision `ADR-MODEL-COMPOSES-CORE`): requirements and units of the code that lives in the core are
`core::REQ-DSO-*` and `core::FU-DSO-*` there. Until that model is published to a registry it resolves
from a checkout of open-dso-core next to this repository, through a workspace file above both, as CI
does:

```sh
cat > ../engmod.work.yml <<'EOF'
schemaVersion: 2
replacements:
- module: github.com/labeth/open-dso-core/model@v0
  path: ./open-dso-core/model
EOF
```

(written from the repository root, so it lands beside `open-sds1000cml` and `open-dso-core`; engdoc
finds it by searching upward, or name it with `ENGMOD_WORK=<path>`).

Run it from `model/`: the code scan resolves `--code-root` from the manifest's directory and the test
scan from the working directory, so only there do both reach the repository and the requirements
trace to their tests as well as their code.

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
