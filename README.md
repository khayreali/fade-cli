<div align="center">

<img src="assets/hero.jpg" alt="fade — book a haircut from your terminal" width="100%">

<br>
<br>

[![Go 1.25+](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20·%20Linux-64748b)]()
[![Made in Brooklyn](https://img.shields.io/badge/made%20in-Brooklyn-ff6319)]()

**Find, compare and book Brooklyn barbershops without leaving your terminal.**

<br>

<img src="assets/terminal.png" alt="fade browsing shops near Graham Av: walk times, prices, ratings, open-now dots" width="94%">

</div>

---

**fade** covers three subway corridors — the **L** from Bedford Av to Halsey St
(Williamsburg, East Williamsburg, Bushwick), the **G** through Greenpoint, and
the **M** through Ridgewood — 74 shops across Booksy, Fresha, Square, Squire,
Vagaro and plain phone lines. The catalog is compiled into the binary, so it
works offline and starts in single-digit milliseconds.

## Highlights

- **Walk-time search** anchored to real subway stops — "what's within a
  20-minute walk of Graham Av", with a radius that widens on its own where the
  map is sparse
- **Open now**, evaluated on the shop's clock (DST-correct), with hours
  sourced per shop — a shop nobody researched shows *unknown*, never a false
  "closed"
- **Every booking link opened and verified by hand.** Directory pages that
  only say "call to book" are never presented as bookable — a test enforces it
- **Manual connectors** for the 27 shops with no booking platform: a
  validated, prefilled call-or-text request, tracked until the shop answers
- **A cut log** that learns your real cadence (median, outlier-resistant)
  and tells you when you're due
- **A real TUI** — arrow keys, adaptive columns, sort by nearest / cheapest /
  best-rated — that degrades cleanly to flags and `--json` for scripts

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
what's nearby: the screen at the top of this page. Arrow keys move, enter
selects.

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
fade-cli book eddo --at "fri 3pm"       # manual connector: prepared call/text
fade-cli appts                          # track those requests
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
| `phone`  | no         | the manual connector (below)     |

Fresha publishes two kinds of page. `/a/` is a partner venue with a booking
flow. `/lvp/` is a directory listing it generates for shops that are *not*
partners — it says so on the page and offers only "Call to book". Only `/a/`
counts as bookable, and a test enforces it. The directory pages are still
useful for hours and services, so they are kept on the shop as `info_url`,
which is never presented as a way to book.

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

**No third-party client can complete a booking here, and that is structural.**
Square's Bookings API is seller-scoped: creating a booking at a shop requires
that shop to OAuth-authorize this application into its own Square account. A
customer cannot obtain those credentials by pasting a token. The same is true
of Booksy, Fresha, Vagaro and Squire, whose APIs exist for merchants running
their own chair.

So the handoff is not a fallback — for a customer-side tool it is the product.
The work that pays off is making the moment of handoff good: knowing whether
the shop is open, whether you can just walk in, and landing on the booking page
rather than a homepage.

### Manual connectors

Twenty-seven shops have no booking platform at all. The manual connector takes
those as close to booked as software honestly can:

```sh
fade-cli book eddo --at "fri 3pm"
```

It validates the time against the shop's real hours — a request for a closed
Saturday is refused with the actual schedule — composes the request ("Hi! I'd
like to book a haircut at Eddo's Barber Shop on Friday Aug 14 around 3pm. My
name is …"), and offers the channels that make sense right now: call first
when they're open, a prefilled text first when they're closed, walking in when
that is the shop's whole model. The message lands on your clipboard either
way, and the request is tracked:

```sh
fade-cli appts               # requested and confirmed appointments
fade-cli appts confirm <id>  # when the shop says yes
fade-cli appts cancel <id>
```

A texted request is recorded as *requested*, not booked — the tool never
claims a chair the shop hasn't promised. `due` shows your next appointment,
and every time it renders is the shop's clock, not your laptop's.

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

- **Opening hours cover 36 of 71 shops.** `o` filters to open-now and browse
  marks each shop ●/○, but only where hours are on file. A shop nobody has
  researched shows no marker at all — never a false "closed". Fresha's venue
  and directory pages carry a full week and were the main source; Booksy
  renders only today's behind a JS toggle, so its 28 shops mostly lack hours.
- **Prices cover 35 of 71, ratings 30.** Booksy and Fresha partner pages
  publish price ranges; directory listings and walk-in shops generally don't.
- **Walk-in policy is recorded for 11 shops only**, always from a source that
  states it. It is never inferred from a shop being a barbershop, because a
  wrong "just turn up" sends someone on a pointless walk.
- **Twenty-seven shops still book by phone.** Each was individually checked
  against Booksy, Fresha, Squire and Vagaro; these publish nothing bookable.
  Two are walk-in only, where a phone number is the correct answer, not a
  fallback.
- **Two salons are included, marked as such.** Roots Radicals and Self Bushwick
  cut men's hair at \$100-120. Hiding them would withhold two of the
  best-rated options in the corridor; merging them silently into a list of
  \$25 fades would misrepresent what a row means. `--type salon` or
  `--type barbershop` filters either way.
- **Booksy models individual barbers as separate businesses.** Five barbers at
  681 Broadway are five catalog entries. `--collapse` folds them by address.
- **Two addresses in the original seed were invented** rather than sourced, and
  both survived every automated check because a confidently wrong value is
  internally consistent. Both are corrected; see Data sources.

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

Two addresses in the original seed were inferred rather than sourced, because a
search named a shop without giving its street. Both geocoded cleanly to
plausible Brooklyn locations near the right stop, so every automated check
passed them — coordinates resolved, precision was exact, drift was zero. A
confidently wrong value is invisible to consistency checks, since it is
internally consistent. Both were caught only by comparing against an outside
source, and both are corrected.

Every rating carries a `rating_src` (`booksy`, `fresha`, `google`, `web`) and
the shop screen shows it. Booksy, Fresha and Google score different populations
on different scales, so a bare number invites a comparison it can't support.
Ratings without a findable review count are stored without one rather than
being given a plausible-looking figure.
