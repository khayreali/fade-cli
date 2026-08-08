# fade cli

Book a haircut from the command line.

Covers three corridors — the **L from Bedford Ave to Halsey St** (Williamsburg,
East Williamsburg, Bushwick), the **G through Greenpoint**, and the **M through
Ridgewood** — with 66 seeded shops across Booksy, Fresha, Square, Vagaro, and
direct booking.

## Install

```sh
make install       # builds and puts fade-cli on your PATH
```

Or just `make build` and run `./fade-cli` in place. One direct dependency,
`golang.org/x/term` for raw-mode key input (which pulls in `golang.org/x/sys`);
everything else is the standard library. The shop catalog is compiled into the
binary, so it works offline and starts in single-digit milliseconds.

## Start here

Run it with no arguments:

```
$ fade-cli
```

First run asks one question — which L stop you'd walk from — then shows you
what's nearby. Arrow keys move, enter selects.

```
  ▌ Near Graham Av
    Williamsburg → Halsey St · 17 shops within a 20 min walk

     Monteman Barber                     5 min        ?  —
     Groomers and Pomade                 6 min        ?  —
     Cabello Brooklyn                    7 min        ?  ★ 5.0 (325)
  ▸  SHEAR 483                           9 min   $55-60  ★ 5.0 (56)
     Power Of Barbers                   11 min   $40-65  ★ 5.0 (89)
     Gentlemen's Barbershop             14 min  $80-145  ★ 4.9 (757)
     Uraga Barber Shop                  14 min   $50-95  ★ 5.0 (129)
     Jack Of All Fadez  +1              15 min   $30-50  ★ 5.0 (178)

    1–12 of 33

  ↑↓ move   ⏎ select   f price   a show all   o open now   s sort   c stop   h history   q quit
```

`s` cycles the ordering between nearest, cheapest and best-rated. Shops with no
price or no rating always sort last — a missing value never wins a ranking by
looking like a zero.

Shops that take walk-ins say so, and when one is open the shop screen leads
with that rather than a phone number — for half the catalog "just turn up" is
the real answer, not "you can't book here".

Shops with known hours are marked ● open / ○ closed, and `o` narrows to what's
open right now. Hours are evaluated in New York time regardless of your
machine's clock.

Browse shows what's actually walkable — a 20-minute radius that widens on its
own where the map is sparse — and `a` switches to the whole directory. Columns
with nothing in them are dropped, so a neighborhood of call-only shops doesn't
render a column of dashes.

Selecting a shop gets you the detail screen — walk time, rating, price, how
they book, and whether you've been before:

```
  ▌ Jack Of All Fadez
    222 Johnson Ave, Brooklyn, NY 11206 · 15 min walk from Montrose Av

  ★ 5.0 (178)   $30-50   books on Booksy

  ▸  Book it         opens in your browser
     See open times  today
     Log a cut here  record what you paid
     Switch barber   2 at this address
     Back

  ↑↓ move   ⏎ select   esc back   q quit
```

### Controls

| Key | Does |
| --- | --- |
| `↑` `↓` or `k` `j` | move |
| `⏎` or `→` | select |
| `esc`, `←`, or backspace | back |
| `1`–`9` | jump the cursor to that row (enter still confirms) |
| `home` `end` `pgup` `pgdn` | jump to ends, page |
| `ctrl-C` | quit, restoring your terminal |

Digits move the cursor rather than selecting outright, so typing `1` on the way
to `10` can't fire the wrong shop.

## Once you know what you want

The flag interface is still there, and it's faster when you know the shop:

```sh
fade-cli again                          # rebook wherever you went last
fade-cli find --under 45 --min-rating 4.9
fade-cli find --sort cheapest           # or: rated, nearest (default)
fade-cli find --to montrose --collapse  # one row per address
fade-cli slots --near graham --day fri  # sweep every shop in range at once
fade-cli book "power of barbers"
fade-cli log add --shop cabello --price 55 --tip 10 --rating 5
fade-cli due                            # when you're next due
fade-cli log --stats                    # where you go, what you spend
fade-cli stops                          # the service area
```

## How booking works

Every shop is bookable on day one. How depends on what the shop uses:

| Kind     | Live times | How you book                     |
| -------- | ---------- | -------------------------------- |
| `booksy` | with a key | deep link to the shop's page     |
| `square` | with a key | deep link to the shop's page     |
| `fresha` | no         | deep link to the venue           |
| `link`   | no         | the shop's own site              |
| `vagaro` | no         | deep link to the venue           |
| `squire` | no         | deep link to the venue           |
| `phone`  | no         | `fade-cli book` dials them       |

### Live availability

`fade-cli slots` shows real open times for any shop whose provider this install has
credentials for. Today that means:

- **Square** — the one genuinely open path. Square documents a
  [Bookings API](https://developer.squareup.com/docs/bookings-api/what-it-does)
  and a shop can OAuth-authorize your app. Set `FADE_SQUARE_TOKEN`, and give the
  shop a `booking.id` of `"<location_id>:<service_variation_id>"` — Square won't
  quote open time without knowing which service you want.
- **Booksy** — their public API issues an `X-API-Key` per business; there is no
  open developer program. Set `FADE_BOOKSY_API_KEY` for shops that have granted
  you access.
- **Fresha** and **Vagaro** — no third-party availability API exists for
  either; their integration programs are merchant-facing. Handoff only.

Shops without credentials degrade to a deep link rather than erroring. That's
the intended state, not a bug — the CLI is useful with zero API access.

**Hairboss Barbershop** (99 Norman Ave, Greenpoint) is the best first target for
a real integration: it hosts its calendar on Square Appointments, which is the
one platform with an open API. It needs a `location_id:service_variation_id`
pair, which requires the shop to authorize the app.

Availability queries fan out concurrently across every shop and provider, so
checking twenty shops costs one round trip rather than twenty.

## Expanding to a new neighborhood

1. Add a `Corridor` to `Corridors` in `internal/geo/geo.go`. Stop ids are
   global, and no two stops may share a street corner — a shared corner splits
   one walkable cluster of shops across two service areas.
2. Append shops to `data/shops.json` with `address` and `booking`, leaving
   `point` out.
3. `fade-cli dev geocode` — fills coordinates from OpenStreetMap, rate-limited to
   OSM's 1 req/sec policy.
4. `fade-cli dev check` — reports what's still missing.
5. `fade-cli dev links` — checks every booking URL still resolves. Hosts that
   refuse scripted traffic are reported as blocked rather than dead, since that
   says nothing about whether a person can book there.
6. `fade-cli dev geocode --force --dry-run` — re-resolves every address and
   reports drift, flagging any shop whose stored point would move to a
   different stop, and any that only matched at street level. Takes about a
   second per shop (OSM's rate limit).

   Queens house numbers are hyphenated (`66-24 Forest Ave`) but OSM indexes
   some streets under the run-together form. Where the canonical address
   doesn't resolve, set `geocode_as` on the shop; the displayed address stays
   canonical.
7. Rebuild to embed the new data.

`fade-cli dev check` is the practical guide to what to research next. It also
warns when `data/shops.json` has been edited since the binary was built — the
catalog is embedded at compile time, so every other command would otherwise
read stale data without saying so.

## Known data gaps

- **Opening hours cover 17 of 69 shops.** `o` filters to open-now and browse
  marks each shop ●/○, but only where hours are on file. A shop nobody has
  researched shows no marker at all — never a false "closed". Fresha venue
  pages carry a full week; Booksy renders only today's behind a JS toggle, so
  the 20 Booksy shops need a different source. Yelp and shop websites cover
  roughly one in six of them; the rest publish hours nowhere machine-readable.
- **Thin at the Bushwick end.** Bedford has 7 shops, Montrose 10, but Grand,
  Jefferson, and Halsey have 1 each — and those are mostly call-only listings
  with no price or rating.
- **Ridgewood is thin at source.** 3 of 16 have hours and 1 has a price; the
  rest are small independents whose only web presence is a phone listing.
  Directory aggregators cover roughly a fifth of them, so further depth there
  needs a different approach (calling, or shop-by-shop social media) rather
  than more scraping.
- **Greenpoint is partly priced.** 3 of 9 shops now carry a price and a rating;
  the rest publish neither anywhere findable. Booksy's Greenpoint search returns
  LIC and Manhattan results, so it was no help.
- **Prices missing on call-only shops.** Booksy and Fresha publish price ranges;
  walk-in shops don't.
- **Booksy models individual barbers as separate businesses.** Five barbers at
  681 Broadway are five catalog entries. `--collapse` folds them by address.

## Development

```sh
make check           # fmt, vet, build, and the race-enabled tests
go test ./...        # unit tests only
```

User state lives in `~/.config/fade/state.json`. Set `FADE_HOME` to relocate it.
Local shop corrections go in `~/.config/fade/shops.local.json` and override the
embedded seed by `id`, so you can fix a price without waiting on a release. An
entry **replaces** the seed shop outright rather than merging field by field —
copy across anything you want to keep, or the rating and hours go with it. Ids
not in the seed are added as new shops.

## Data sources

Shop data was compiled from public Booksy, Fresha and Vagaro listings plus shop
websites, and geocoded via OpenStreetMap Nominatim. Ratings and prices are
point-in-time snapshots — verify before relying on them.

Every rating carries a `rating_src` (`booksy`, `fresha`, `google`, `web`) and
the shop screen shows it. Booksy, Fresha and Google score different populations
on different scales, so a bare number invites a comparison it can't support.
Ratings without a findable review count are stored without one rather than
being given a plausible-looking figure.
