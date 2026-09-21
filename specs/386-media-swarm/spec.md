# 386 — Safe reproducible CLI media

## Requirements (fixed before implementation)

- Never delete or reuse an unknown sandbox. Two simultaneous recorders must not
  delete each other's files or corrupt the same output set.
- Execute current `wa`/`wad` binaries in a fresh unpaired HOME and all XDG roots,
  without inherited account/remote configuration. No pairing, sends, revokes,
  history reads, production services, or uploads.
- Produce genuine GIF, MP4, WebM, PNG fallback and text equivalent from the
  existing VHS tape; retain the unpaired warning and truthful build identity.
- GIF <=2 MiB; each video <=5 MiB; PNG <=1 MiB; 1100x640; 10–60 seconds;
  no audio. Inspect representative frames. Reproduce via the existing dev shell.
- Leave a runnable ownership/concurrency regression and actual media checks.
- OpenAI implementation after verified GLM failure; independent GLM review and
  all merges/deploys belong to the coordinator, not this worker.

## Examples and properties

Given an occupied output lock, another recording exits nonzero and leaves it
untouched. Given a failed build, only the invocation's acquired resources are
removed. Universally, cleanup only removes resources successfully created by
that invocation. Given an unpaired demo, status is disconnected and doctor warns;
no frame may imply a paired account or a successful real-world mutation.
