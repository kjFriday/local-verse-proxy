# Ledger: release (chunk C4)

The downloads (`.github/workflows/release.yml`). Format and rules:
README.md here, spec section 19. go-port's release made six archives
of `mint-key` with README.md and seats.env.example.

```text
ID      R-1
What    Five archives, not six: windows-amd64.zip, windows-arm64.zip,
        one macos.tar.gz with universal programs (Intel and Apple
        silicon in one file, by lipo), linux-amd64.tar.gz,
        linux-arm64.tar.gz; and SHA256SUMS. Each has one folder,
        local-verse-proxy/, with lvp, mint-key (this release only),
        THIRD_PARTY_NOTICES.txt and the source tree of the tag but
        .github/ and tests/ (compose builds the image from it).
Why     Spec 4.1, decision 22: one download per system.
Safe    The same programs; a Mac user cannot pick the wrong one.
Tests   release.yml `build-macos` (lipo -info lists x86_64 and
        arm64), `try` (each archive unpacked and run on its own kind
        of runner)
```

```text
ID      R-2
What    Archives are reproducible: every file has the tag's commit
        time, tar lists files by name with owner 0, gzip writes no
        name or time, zip lists a sorted file list with no extra
        fields. The job packs twice and fails if SHA256SUMS differ.
Why     Decision 23.
Safe    Anyone can rebuild and compare.
Tests   release.yml `pack`
```

```text
ID      R-3
What    The release is a draft: the job does not publish it.
Why     Publishing is the user's call (spec 15.3, 23).
Safe    Nothing reaches users without a person's click.
Tests   release.yml `publish` (gh release create --draft)
```

```text
ID      R-4
What    The release checks that the pinned images (LiteLLM v1.106.6,
        postgres:16-alpine, golang:1.27.3-alpine3.24) still list
        linux/amd64 and linux/arm64, and fails if one lost a platform.
Why     Gap 6: arm64 Linux and Windows on Arm run the whole stack.
Safe    A check more.
Tests   release.yml `pack`
```

```text
ID      R-5
What    Archives carry THIRD_PARTY_NOTICES.txt (Go's license and each
        linked module's, from internal/notices); there is still no
        license file of the project's own.
Why     Decision 24; the license is the user's call (README: "To be
        decided").
Safe    Compliance for the linked code; nothing chosen for the user.
Tests   release.yml `pack`, `try` (the file is there)
```
