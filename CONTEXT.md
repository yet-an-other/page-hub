# Page Hub

Page Hub manages the material published through `share.bdgn.me` while keeping each publication publicly accessible at its own URL.

## Language

**Project**:
A managed grouping under one top-level URL path. A Project can contain multiple Publications and may have its own private description.
_Avoid_: Using Project as a synonym for Publication

**Publication**:
A public item managed as one unit within a Project. It may contain one file or a complete static site and may have its own private description.
_Avoid_: Project, artifact

**Root Publication**:
A Publication located directly at its Project's top-level path rather than beneath a Publication path.
_Avoid_: Project

**Adoption candidate**:
A storage layout that Page Hub has discovered but has not accepted as a Publication. It remains unmanaged until Page Hub validates and accepts it.
_Avoid_: Publication

**Replacement**:
A content change that treats the submitted file or directory as a Publication's complete desired contents. It preserves the Publication's location, entry point, and routing behavior.
_Avoid_: Update, sync, merge

**Drift**:
A difference between a Publication's accepted state and its objects as observed in storage. Drift does not change accepted state by itself.
_Avoid_: Update, accepted change
