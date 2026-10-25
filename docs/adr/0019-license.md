# ADR 0019: Elastic License 2.0 from this release on

Status: accepted (the owner's decision; have it reviewed by a lawyer before relying on it).

## Context

The owner wants individuals and companies to run Payrudder for their own applications, but not to sell it to third parties as a hosted or managed service; the owner may. The repository is Apache-2.0 today, which allows exactly that resale.

## Decision

New versions are licensed under the **Elastic License 2.0**: use, copy, modify and redistribute for any purpose except (1) providing the software to third parties as a hosted or managed service that gives access to a substantial set of its features, (2) circumventing licence-key functionality, (3) removing notices. It is a source-available licence, not an open-source one by the OSI definition. Versions released before the change (v0.1.0 to v0.4.0) remain available under Apache-2.0 to those who received them.

## Consequences

- `LICENSE` is replaced, a `NOTICE` records the change and the dependencies' licences, and the README says "source-available".
- Contributions need a short contributor agreement that lets the owner relicense them; until one exists, outside contributions are not merged.
- Dependencies keep their own licences; none is copyleft in a way that conflicts (checked in CI by a licence report).
