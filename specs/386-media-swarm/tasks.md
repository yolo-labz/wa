# Tasks

- [x] T1 `scripts/record-demo.sh`, `scripts/test_record_demo.py`: owned temporary
  directories, output exclusion, failure cleanup; four unittest subprocess
  regressions passed (`docs/assets/wa-demo-evidence/ownership-tests.txt`).
- [x] T2 `docs/assets/wa-demo.tape`, `scripts/record-demo.sh`,
  `scripts/check-demo.sh`, generated `docs/assets/wa-demo.{gif,mp4,webm,png,txt}`:
  actual isolated recording; metadata/budgets/full decode PASS, oversized-GIF
  rejection PASS; frames at 1/7/10/14 seconds inspected (`wa-demo-evidence/`).
- [x] T3 `README.md`, `docs/assets/wa-demo.md`,
  `docs/swarm-2026-09-21.md`, `docs/assets/wa-demo-evidence/`: reproduction,
  accessibility equivalent, provenance and honest verification evidence.
  Full race/shuffle run: 33 packages passed, exit 0 (`wa-demo-evidence/go-tests.txt`).
- [ ] T4 Coordinator: exact-head independent full GLM gate after capacity recovers;
  PR/merge serialization. Worker must not self-certify this gate.
