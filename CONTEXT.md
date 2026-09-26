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

**Rename**:
A change to a Project's or Publication's private display name. It never changes a public URL or a stored object.
_Avoid_: Using Rename for a URL change

**Move**:
A change to a Publication's public URL, by giving it another Project, another Publication path, or both. Old links stop working and no redirect remains.
_Avoid_: Rename, relocate

**Project move**:
A change to a Project's top-level prefix. It moves every Publication in the Project.
_Avoid_: Project rename

**Legacy writer**:
The direct-storage `web-share` publishing path that stays active until the publishing cutover revokes its credential. Its writes are expected changes, not evidence of a compromised credential.
_Avoid_: Direct writer, old writer

**Unclaimed object**:
A stored object that no accepted manifest claims. It counts toward usage and collision checks but belongs to no Publication.
_Avoid_: Orphan, stray, unmanaged file

**Drift**:
A difference between a Publication's accepted state and its objects as observed in storage. Drift does not change accepted state by itself.
_Avoid_: Update, accepted change

**Route probe**:
A declared public-route check: a plain GET of one public URL that must return the expected status. Planning records the observed status, content type, and body digest in the digest-bound plan; committing repeats every probe immediately before the catalog transaction.
_Avoid_: Health check, ping
