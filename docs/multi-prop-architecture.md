# Supporting more props: architecture analysis

Flowa started as a nunchaku tracker. This note covers what already works for other props
(poi, staff, double staff, buugeng), what is still nunchaku-specific, and the order to fix it in.

The prop knowledge here (how each prop is structured, which move families exist) is from
general knowledge. It has not been researched against sources the way the nunchaku map was.

## Update: steps 1 to 3 are done

All five props now have researched catalogs, loaded per prop (`web/catalogs.js`), and
progress is recorded per variant (`one`, `LR`, `FR`). Plans and gates come from each catalog.
Demos now cover all five props (5). Still open from the list below: shared concepts across props
(4), cross-prop views (7), storage growth (8) and a catalog editor (9). The catalog editor is
planned in the MOD epic ([epic-mod/todo.md](epic-mod/todo.md), milestones M5 and M6), and the
shared motion fields that would support shared concepts in the GEO epic
([epic-geo/todo.md](epic-geo/todo.md), M3). See [roadmap.md](roadmap.md) for the order. The table
below describes the state before this work.

## Where things stood

| Layer | Multi-prop today? | Notes |
| --- | --- | --- |
| Accounts, sessions | Yes | Postgres; users choose their props at first sign-in |
| Progress storage | Yes | One JSON document per (user, prop), with its own revision |
| Practice log | Yes | Works for every prop, with or without a skill map |
| Prop registry | Partly | `web/props.js` lists five props; only nunchaku is `ready` |
| Skill catalog | No | `web/data.js` and `web/details.js` are nunchaku globals |
| Sidedness | No | "Left/right hand" is decided by nunchaku branch letter |
| Plan and gates | No | Phases and the 12-week track are nunchaku content |
| Demos | No | One striker on a cord, one wrist model per move |
| Combos | Partly | Stored per prop, but pick skills from the nunchaku catalog |

## What differs between props

| Prop | Objects | Body | What a skill varies by |
| --- | --- | --- | --- |
| Nunchaku | 1 (or 2) | Two rigid sticks on a cord | Hand, grip, plane, direction |
| Poi | 2 | Point mass on a tether | Timing, direction, plane, hand path |
| Staff | 1 | Long rigid body, held or in contact | Hand, grip position, plane, contact point |
| Double staff | 2 | Two rigid bodies | Timing, direction, plane (close to poi theory) |
| Buugeng | 2 | Two curved rigid bodies | Relative rotation and symmetry of the pair |

Three consequences:

- **"Left hand / right hand" is not universal.** Paired props are trained as a pair, and what
  varies is timing and direction (same or split time, same or opposite direction).
- **Much theory is shared.** Planes, timing and direction, weaves and isolations appear in
  several props. The wiki already describes the nunchaku weave as adapted from poi.
- **Contact and throws differ most.** They depend on the prop's shape, so they need
  per-prop guides and per-prop physics.

## Development areas, in order

1. **Catalog as data.** Move each prop's branches, skills, prerequisites, guides, references,
   phases and gates into one JSON file per prop, loaded on demand. Give it a version, so
   progress can be migrated when a map changes. This removes the nunchaku globals and is
   the step everything else depends on.
2. **Variants instead of hands.** Replace the hard-coded left/right rule with a per-skill
   `track` field: `LR` (each hand), `single` (one level), or a named set such as the four
   timing-and-direction modes. Progress is then stored per variant, and "unlocked" checks
   name the variant they need.
3. **Content pipeline.** Each new map needs the research the nunchaku one got: sources,
   names, prerequisites, guides, and honest marking of what is unconfirmed. Do one prop
   at a time; poi first, because double staff reuses most of its theory.
4. **Shared concepts.** Add a small cross-prop layer (planes, timing and direction, weave,
   isolation) that skills can reference, so learning one prop can suggest ready skills in
   another. Keep it optional: wrong transfer claims are worse than none.
5. **Demo engine.** The simulation core (rigid body or point mass, tether, gravity, a hand
   that follows the prop) already fits poi with fewer parts. Staff and buugeng need a held
   rigid body, then contact with the body, then releases. Keep the rule that a clip ships
   only when `test/physics.js` passes.
6. **Plans per prop.** Phases and gates belong in each prop's catalog. The contest track is
   nunchaku-specific and should stay optional.
7. **Cross-prop views.** A combined dashboard (total practice, streak across props), and
   combos that mix props, once two maps exist.
8. **Storage growth.** JSON documents are fine for one household. If sharing, coaching or
   statistics are wanted, split levels and sessions into their own tables. Add scheduled
   `pg_dump` backups and HTTPS before the app is reachable from outside the LAN.
9. **Admin tooling.** A catalog editor and import/export, so maps can be corrected without
   a redeploy.

## Risks

- **Content accuracy** is the main one. A plausible-looking map with wrong prerequisites
  teaches bad habits. The draft families in `web/props.js` are placeholders, not a plan.
- **Naming varies** between communities; catalogs need aliases.
- **Catalog changes orphan progress** unless skill IDs are stable and versions are migrated.
- **No email on file** means a forgotten password can only be reset by the admin.
