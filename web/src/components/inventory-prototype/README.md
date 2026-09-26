# Inventory layout prototype

Throwaway UI study, not production code. Based on the accepted first prototype at `4361f43` and the inventory implemented in #20.

Question: how can the grouped inventory show storage observations without crowding out Publications and their descriptions?

## Run

From `web/`, run `pnpm prototype`. Open http://127.0.0.1:5174/?variant=A.

- `A`: the grouped inventory, with descriptions beneath one-line preview links and compact metadata columns. Long links end in an ellipsis; descriptions wrap without truncation.
- `B`: a reading list, with descriptions beneath titles and no table columns.
- `C`: a Project index beside the selected Project's Publications.

Use the floating arrows or the keyboard's left and right arrows. `/` focuses search. The `…` button opens sample-state controls and the full current state.

The normal `/` route retains its TanStack Query calls. Explicit `prototype` mode replaces API responses with synthetic data, stubs Refresh, and swaps the rendered subtree. Normal production builds do not mount the prototype or its switcher. `pnpm prototype:build` builds a public-safe static demo into `web/dist-prototype/`.

All variants put usage, connectivity, scan freshness, and Refresh in the header. Exact byte counts and full observations are available from the header's storage button. State badges remain on one line; selecting one opens the Publication's observation details. Descriptions remain visible at narrow widths. The table switches to stacked rows rather than horizontal scrolling.

Sample data includes ten Publications, long descriptions and paths, an uppercase entry point, drift, missing content, an empty description, and an empty Project. Sample-state controls include all-in-sync, stale, unavailable, and before-first-scan cases. The sample public origin is `example.invalid`. Preview and direct-link controls open explanatory local demo pages, never live content.

Public demo: https://share.bdgn.me/page-hub/inventory-layout/?variant=A

Source branch: `prototype/inventory-layout`. This branch is the primary source, not a production patch.

Checked in Chromium at 320, 390, 620, 768, 860, 1024, and 1440 pixels. No horizontal overflow in any variant. Search, collapse restoration, attention filtering, keyboard switching, exact storage values, observation dialogs, Project selection, and simulated link destinations were exercised. Axe reported no violations for the three default variants at desktop and mobile widths. Typecheck and lint pass.

No catalog or storage writes. No private catalog data. No persistence.

The operator preferred A and requested removing its description column. A now puts each multiline description directly beneath its single-line preview link. The revised prototype remains separate from production for review.
