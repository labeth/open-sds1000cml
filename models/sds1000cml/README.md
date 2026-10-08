# SDS1000CML+ engineering model

The documentation of the firmware: an engineering model (schema version 2) in
[`model/`](model/), with [`engmod.yml`](engmod.yml) as its manifest.

| File | Contents |
|---|---|
| `requirements.yml` | requirements (EARS), with stable `REQ-SDS-*` IDs cited from the code (`TRLC-LINKS`) |
| `decisions.yml` | design decisions (`ADR-*`): context, decision and consequences, including bench results |
| `architecture.yml` | functional units, hardware items and interfaces (`ENGMODEL-OWNER-UNIT` in the code) |
| `behavior.yml` | runtime flows |
| `views.yml` | architecture views |
| `catalog.yml`, `compliance.yml`, `assurance.yml` | supporting catalogues and assurance records |

Changes to behaviour go through the model first: record the decision in
`decisions.yml`, then change the code.
