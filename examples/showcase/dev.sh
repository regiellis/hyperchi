#!/bin/bash
# HyperChi showcase dev server. Thin wrapper around the Taskfile so there is one
# source of truth. Prefer `task dev` directly; this exists for muscle memory.
exec task dev "$@"
