# Third-party components

The released `ME7_LaunchControl.exe` embeds the following tools **unmodified**.
They are not stored in this repository; `scripts/fetch-tools.*` downloads them
from pinned upstream sources and checks their SHA-256 (`scripts/tools.sha256`).

| Component | Author | Source | License |
|---|---|---|---|
| `ME7Info.exe` v1.20 (part of ME7Logger) | mki (setzi62) | [nyetwurk/ME7L](https://github.com/nyetwurk/ME7L) @ `89d0ff9` | Freeware, **non-commercial use only** - [third_party/ME7Logger-LICENSE.txt](third_party/ME7Logger-LICENSE.txt) |
| `defs/me7_std.map`, `defs/me7_alias.map` | ME7Logger distribution | [nyetwurk/ME7L](https://github.com/nyetwurk/ME7L) `defs/dist` | as above |
| `me7sum.exe` v1.1.7 | 360trev, nyet | [nyetwurk/ME7Sum](https://github.com/nyetwurk/ME7Sum/releases/tag/v1.1.7) | MIT - [third_party/ME7Sum-LICENSE.txt](third_party/ME7Sum-LICENSE.txt) |

**Because ME7Info is licensed for non-commercial use only, and may not be sold
or distributed for a fee, the same restriction applies to any build of this
tool that embeds it.**

## Launch control code

The injected launch control / NLS routine and the logic for finding the
variables, code cave and hook come from `launch.php`, a script shared in the
ME7 tuning community (NefMoto). Its original author and license are unknown.
`patch.go` is a Go port of it that produces byte-identical output on files the
original handled correctly. If you are the author of `launch.php`, please open
an issue so you can be credited properly.
