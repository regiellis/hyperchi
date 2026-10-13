---
title: Drawing light without a light engine
date: 2025-12-03
author: Tobi Achterberg
tags: art, tech
game: lamplit-crossing
summary: Every glow in The Lamplit Crossing is painted, not computed. How we fake lantern light with three layers and a lot of restraint.
---

*The Lamplit Crossing* is a game about light, and it has no dynamic lighting.
Every glow you see is a painted shape on one of three layers. Here is why, and
how it holds together.

## Three layers

Each scene has a dark base painting, a glow layer, and a spill layer. The base
is painted as if every lamp were out. The glow layer holds the soft halos
around lanterns. The spill layer holds light landing on things: wet planks,
the side of a crate, a passenger's face.

When a lantern dims, we fade its glow and spill shapes together. Nothing is
calculated per pixel, so the game runs on a Steam Deck at a steady 60 frames
per second with room to spare.

## Rules we paint by

- One warm light color per scene. A second color reads as a mistake.
- Light falls off fast. A lantern lights a circle, not a room.
- Shadows are where the story is. We leave more of the screen dark than feels comfortable.

## What it costs

Painting spill by hand for every prop in every harbor takes time: about two
days per island. We think the result is worth it, and it means the art team
(that is, me) decides exactly where your eye goes.
