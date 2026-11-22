# Releasing

The version lives in one place: the `VERSION` file in the repository root (for example `2.1.0`). The footer, the image
labels and the release all read it, so there is nothing else to keep in step.

For a new version, say `2.2.0`:

1. Create the integration branch `v2.2.0` from `main`. Each Jira case gets its own branch off it and is merged back.
2. On that branch, change `VERSION` to `2.2.0` and start a `## 2.2.0` section at the top of `CHANGELOG.md`.
   `scripts/check-version.sh` (run by CI) fails if the two disagree or if a copy of the version appears anywhere else.
3. When the work is done and tested, merge the branch to `main` and tag it: `git tag -a v2.2.0 <commit>`, then push
   `refs/tags/v2.2.0`. The release workflow checks that the tag matches `VERSION` before it publishes anything.
4. Never move a published tag. A fix after a release is a new version.
