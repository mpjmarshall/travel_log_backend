## GEOCODE-1 — Kyoto came back in its own script, and was stored that way

The client's integration suite found it: T5's search for **Kyoto** offered one
city, and it was called **京都市**. Picking it stores that as the city's name for
good, in an app whose every other word is English.

**Cause.** `internal/geocode/geocode.go` sent Photon `q` and `limit` and no
`lang`, so Photon answers each place in its default name, which is the local
one. Measured against `photon.komoot.io` on 13 September 2026:

| query | no `lang` | `lang=en` |
|---|---|---|
| Kyoto | `京都市` | `Kyoto`, Japan |
| Munich | — | `Munich`, Germany |
| Moscow | — | `Moscow`, Russia |
| Porto, Lyon, Bordeaux, Lisbon | Latin script already | unchanged |

So the defect only shows for a city whose local name is in another script, which
is why every earlier search anyone tried looked right.

**Fix.** `Search` sends `lang=en`, from one constant. English rather than the
device's language, because the app has no localisation and a city stored in
German on one phone would be the same defect on another.

**Guarded.** `TestNamesAreAskedForInEnglish` asserts the parameter reaches the
geocoder. Mutation-proved by file copy, per ground rule 7: deleting the `lang`
line reddens exactly that leg; restoring with `cp` and `cmp -s` greens it.

**Not done here.** A city already stored in its local script keeps its name;
nothing rewrites stored rows. And the running `travellog-live` stack serves the
old behaviour until it is rebuilt from this commit.
