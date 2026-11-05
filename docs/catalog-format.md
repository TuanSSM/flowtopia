# Catalog format

One JSON file per prop, `web/catalog/<prop>.json`. Check it with `node test/catalog.js <file>`.

```json
{
  "id": "poi",
  "name": "Poi",
  "about": "One or two sentences on where the names and definitions come from.",
  "careNote": "Shown on skills marked care: true. One sentence of safety advice for this prop.",
  "branches": [{ "k": "F", "name": "Foundations", "blurb": "Short description" }],
  "skills": [{
    "id": "F1", "tier": 1, "name": "Skill name", "desc": "One factual sentence saying what it is.",
    "needs": ["F2", "W3!"], "track": "one",
    "star": false, "care": false,
    "steps": ["Step one.", "Step two."], "watch": ["A common mistake"], "drill": "One drill.",
    "refs": ["wikiPoi"]
  }],
  "refs": { "wikiPoi": ["Wikipedia: Poi (performance art)", "https://en.wikipedia.org/wiki/Poi_(performance_art)"] },
  "phases": [{ "id": "p1", "name": "Phase name", "time": "1 to 2 months", "skills": "F1 F2", "gate": "A test you can pass or fail.", "req": ["F2"] }]
}
```

Rules:

- **Branches:** 5 to 11, each with a single capital letter `k`, unique.
- **Skill ids:** the branch letter plus a number (`F1`, `W12`), unique. Tiers are 1 (foundation) to 5 (expert).
- **needs:** ids of other skills in the same file. No cycles. A trailing `!` means every tracked variant must be clean.
- **track:** how progress is recorded. `one` = a single level. `LR` = left hand and right hand separately. `FR` = forward and reverse direction separately.
- **star: true** when the name or description is not confirmed by a source that was actually opened.
- **care: true** when the move is risky enough to need the careNote.
- **steps, watch, drill:** 1 to 5 steps, 1 to 3 things to watch for, one drill. Where no opened source describes the mechanics, say so in the first step and do not invent them.
- **refs:** keys into the file's `refs` map. Every URL must be one that was opened successfully.
- **phases:** 3 to 6, ids `p1`, `p2`, ... Every id in `skills` and `req` must exist.
