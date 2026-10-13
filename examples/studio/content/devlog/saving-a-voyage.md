---
title: Saving a voyage in 2 KB
date: 2026-06-24
author: Mara Ilves
tags: tech
game: lamplit-crossing
summary: A whole night of The Lamplit Crossing fits in a save file smaller than this page's favicon. How we keep it small and why it matters.
---

You can stop *The Lamplit Crossing* in the middle of a storm and pick it up on
another machine. That works because a save is tiny: about 2 KB for a full
winter.

## Store decisions, not state

We do not save the position of every wave. The sea is generated from a seed and
the time of night. A save holds the seed, the clock, the route you chose, the
cargo, and a short list of things that happened. Everything else is rebuilt.

```text
seed      4-byte integer
clock     minutes since dusk
route     island ids, in order
cargo     item ids with counts
events    (night, island, event id) triples
```

## Versioning

Each save starts with a format number. When we change the format, the loader
upgrades old saves one version at a time, and every old format has a test
file in the repository that must still load.

## Why it matters

Small saves sync instantly, survive cloud storage limits, and make bug reports
easy: players attach the save, and we replay their night exactly.
