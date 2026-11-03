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
[engineering-model-go](https://github.com/labeth/engineering-model-go), at the commit pinned in
`.github/workflows/ci.yml`:

```sh
engdoc --model model/engmod.yml --requirements model/requirements.yml \
  --design model/views.yml --code-root .. --out /tmp/ARCHITECTURE.adoc
```

Changes to behaviour go through the model first: record the decision in
`decisions.yml`, then change the code.
