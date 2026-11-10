---
name: screenshots
description: Take the pictures CI takes, locally - the app pages at desktop and phone width and a contact sheet of chosen demos - and look at them. Use to check a UI or figure change visually, or when asked to "show me" a page or demo.
---
Both scripts start a dev server on :3170 with a throwaway database; the real data is never touched.

- Pages (what the PR comment shows): `test/browser/run.sh pages.py /tmp/shots`, then read
  `/tmp/shots/index.json` for the list and open the PNGs you need with the Read tool.
- Chosen demos: `SZ=400 COLS=4 test/browser/run.sh shots.py /tmp/sheet.png <tile> ...`
  where a tile is `prop,skill,variantIndex,view,phase`, for example `nunchaku,F5,0,angle,0.15`,
  `poi,L2,0,front,0.5`, `nunchaku,F1,0,angle,0` (a close-up). Views: `front`, `angle`, `side`.
  Phase is 0 to 1 through the loop. A tile that says "Loading the demo…" needed more `WAIT`.
- Read the image and describe it concretely (what the hand holds, where the elbow points,
  whether text overflows) before judging. Mention the path.
- Finish with `docker rm -f flowa-test-db`.

On the GitHub runner the same pictures come from `pages.py` under Xvfb and land on the
`ci-screenshots` branch; a page added to `pages.py` appears in every PR summary from then on.
