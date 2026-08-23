# AGENTS

<!-- ward:protocol v4 -->
## WARD — verified project memory (managed block; do not edit between markers)

This project is ward-managed. Follow this protocol exactly; it exists so you
never re-solve solved problems and never trust stale claims.

1. SESSION START (always, before planning): run

       ward brief [topic]

   It re-verifies store-local results live, frees expired reservations, and
   prints prior knowledge, open runs, active claims, the task pool, and
   suggested next actions. Do what it says before planning.
2. SPEC-FIRST: for anything beyond a trivial edit, draft the spec before
   code: .spec/<topic>.md with Purpose / Signals / What's kept / What's
   changed and why / Open questions. Develop incrementally and keep the spec
   honest as you close each increment - a stale spec is a stale claim.
   Load relevant domain knowledge before writing it (see chips below).
3. TRUST RULE: only verified artifacts are facts. A memory hit votes for the
   cheap tier ONLY when live-verified against repo state; unverified, stale, or
   imported artifacts count as a MISS -> work at full attention. Treat a
   routing decision's verified context ids as truth, never a recap.
4. WORK FROM THE POOL (loop, do not ask permission): while brief lists open
   tasks within your budget —

       ward task next --by <your-name> --max-tier <budget>

   implement the pulled task's title in this repo, prove it with

       ward task run <task-id>

   then repeat until the pool is empty or every remaining task is beyond your
   ability or blocked. On failure the task re-enters the pool one tier higher —
   do not retry it yourself; pull different work or stop. To resume a task a
   dead session left claimed: ward task take <id> --by <your-name>.
   When nothing is left: ward memory handoff, then stop.
5. EXCLUSIVE WORK: before touching a shared topic outside the pool (file,
   migration, release), run: ward memory claim add <topic> --ttl 60
   A conflict is a hard stop: pick different work, never proceed in parallel.
6. RECORDING RESULTS IS AUTOMATIC: successful runs capture store-local
   artifacts tagged by node id. Do NOT hand-type ward memory put; never write
   a verify_cmd you would not run yourself.
7. BEFORE ENDING: run  ward memory handoff  so the next session inherits
   incomplete work, open runs, and stale candidates.
8. FAILURE POLICY: two escalating failures exhaust the budget and the run
   stops for a human with a dossier (ward reject <run>). Never retry past it.

PORTABLE KNOWLEDGE: accepted lessons tagged portable:<topic> are synced to
the global skills directory by 'ward skill-sync' - load any that match your
task before planning.

Every command accepts --json for machine-readable output. If a command errors,
fix the cause; never bypass the store or the trust boundary.
<!-- /ward:protocol -->
