# bori Architecture

## Overview

bori sits between dev tools and observability. It does not own business logic — it composes and connects.

```
App repos (JUMI, tori, sori, NodeVault, ...)
  each with .bori/component.yaml + .bori/policy.yaml
        ↓
bori/devspace.yaml  (imports components)
        ↓
DevSpace inner loop (dev/deploy/sync)
        ↓
bori/adapters/devspace/run-gate.sh
        ↓
kube-slint --profile devspace
        ↓
PASS / FAIL → dev-space observability page
```

## Self-registration convention

Each dataplane app repo that wants to participate in the bori dev loop adds:

```
<app-repo>/
└── .bori/
    ├── component.yaml   # DevSpace component spec
    └── policy.yaml      # kube-slint SLI thresholds per profile
```

bori discovers these files at runtime. No central registry to update.

## Adapter interface

The adapter contract (defined in kube-slint):
- Receive: profile name, list of policy files
- Execute: smoke metrics collection → slint-gate evaluation
- Return: PASS / FAIL + summary JSON

bori provides the concrete implementations:
- `adapters/devspace/` — triggered by DevSpace hooks
- `adapters/tilt/` — triggered by Tilt (planned)

## Profile model

| Profile | Environment | Notes |
|---------|-------------|-------|
| `devspace` | DevSpace inner loop on K8s VM | primary target |
| `kind` | local kind cluster | CI / offline |
| `multipass` | Multipass VM | existing vm-lab |

Each app declares per-profile thresholds in `.bori/policy.yaml`.

## Relationship to batch-integration

`batch-integration` continues to own:
- vm-lab smoke scripts (multipass profile)
- dev-space observability publish
- JUMI/AH specific integration scripts

bori does not replace these — it adds the DevSpace/Tilt adapter layer alongside them.
