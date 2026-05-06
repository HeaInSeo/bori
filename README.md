# bori — Build, Orchestration, and Runtime Integration

bori is the developer environment orchestrator for Kubernetes-native dataplane applications.

It bridges inner-loop dev tools (DevSpace, Tilt) with SLI observability (kube-slint),
and composes multiple dataplane apps without requiring each app to know about the others.

## Core principles

- **Self-registration**: each app declares itself via `.bori/component.yaml` and `.bori/policy.yaml` in its own repo. bori does not maintain a hardcoded app list.
- **Tool-agnostic core**: kube-slint remains independent. DevSpace and Tilt connect through adapters.
- **Extensible**: adding a new dataplane app does not require changes to bori itself.

## How it works

```
App repo (JUMI, tori, sori, ...)
└── .bori/
    ├── component.yaml   # DevSpace component definition for this app
    └── policy.yaml      # kube-slint SLI thresholds for this app

bori/
├── devspace.yaml        # imports component.yaml from each app repo
└── adapters/devspace/   # translates DevSpace lifecycle → kube-slint gate
```

When a new app is ready to join:
1. The app repo adds `.bori/component.yaml` and `.bori/policy.yaml`
2. `devspace.yaml` adds one import line
3. The adapter picks up the new policy automatically

## Adapters

| Adapter | Status |
|---------|--------|
| DevSpace | in progress |
| Tilt | planned |

## Relationship to kube-slint

bori does not replace kube-slint. kube-slint defines the adapter interface and runs the gate evaluation. bori adapters are the concrete implementations that translate dev tool lifecycle events into kube-slint invocations.

```
devspace dev
    ↓ hook
bori/adapters/devspace
    ↓ invokes
kube-slint --profile devspace
    ↓
PASS / FAIL
```

## Directory structure

```
bori/
├── devspace.yaml          # base compose, imports app components
├── adapters/
│   ├── devspace/          # kube-slint DevSpace adapter
│   └── tilt/              # kube-slint Tilt adapter (planned)
├── schema/
│   ├── component.schema.yaml   # spec for .bori/component.yaml
│   └── policy.schema.yaml      # spec for .bori/policy.yaml
├── docs/
│   └── architecture.md
└── example/
    └── .bori/
        ├── component.yaml      # reference implementation
        └── policy.yaml
```
