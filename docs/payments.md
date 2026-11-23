---
title: "Payments kit"
order: 46
---

# Payments kit

A project that sells something uses the **payments kit**: one provider-neutral interface for checkouts (one-off and subscription), webhooks turned into neutral events, refunds, payment lookups and an invoice hook. Today the kit has one provider, the **stand-in**, which takes no money and needs no account. A real provider (Stripe, Paddle, Przelewy24, Tpay; research report o-20261120-193a) comes later as one adapter plus config, and the app doesn't change.

## Where it lives and why

The kit is a **small service the project runs beside its app**, `payments-kit serve`. The app talks to it over local HTTP with JSON. The projects are built on different stacks (a TypeScript/Node PWA, a Godot game, others), so:

- **A service, not a library per stack.** Any stack can call HTTP, so each provider adapter is written and tested once, not once per language. Provider keys stay in the kit's process, and the app never reads a provider's webhook format: it gets one event shape, signed one way.
- **Not code copied into each project.** A copy per project would miss every later fix.
- **The code:** `internal/payments` (the interface, the types, the service), `internal/payments/standin` (the stand-in provider), `internal/payments/paymentstest` (the contract every adapter passes) and `cmd/payments-kit` (the binary). It uses the standard library, yaml and one leaf helper (`internal/jsonfile`, the repo's atomic JSON write), so it can move to a repository of its own with that helper copied along. The owner picks its licence before any live payment.

**Per project, never in the kit:** products, prices (VAT-inclusive per country), the orders and what a payment unlocks (entitlements), terms (regulamin), the privacy text, the success and cancel pages, and the invoice text. The kit only reports what happened. The 14-day withdrawal *flow* (waiver record, confirmation message, withdrawal and cancel pages) is in the kit; its *wording* is the project's reviewed text pack ([Consent, withdrawal and cancel](#consent-withdrawal-and-cancel)).

Store purchases (App Store, Google Play) are outside the kit: the store is the merchant there.

## Enable it in a project

1. Build it: `go install github.com/kacperwerema/dark-factory/cmd/payments-kit@latest`, or `go build -o payments-kit ./cmd/payments-kit` in this repo (`make install` builds only issue-cli and issue-viewer).
2. Write `payments.yaml` in the project (shape below) with `provider: standin`.
3. Create the secrets the config names (`env:` variables, or Keychain items: `security add-generic-password -s <service> -a <account> -w`, typed by the owner).
4. `payments-kit check -config payments.yaml` lists each problem and each secret as set or missing, never its value.
5. `payments-kit serve -config payments.yaml`, and the app calls it.

A project with a Sales department reaches the kit through two connectors (below, [From a department](#from-a-department)); the Sales template's [When you start charging](departments.md#when-you-start-charging) block is the process: every checkout, price change and refund passes an `owner_only` gate. Installing the kit into a project's own tree is a later step.

### Config

```yaml
provider: standin          # an adapter's registered name: payments-kit providers
mode: sandbox              # live is the owner's switch; the stand-in refuses it
listen: 127.0.0.1:8787     # the default
public_url: http://127.0.0.1:8787     # where buyers and the provider's webhooks reach the kit
events_url: http://127.0.0.1:3000/api/payments/events   # the app's endpoint for events
state_dir: .payments       # handled.jsonl and invoices/, relative to this file
invoice: record            # none (default) | record
secrets:                   # references only: env:<VAR> or keychain:<service>[/<account>]
  api_token: env:PAYMENTS_API_TOKEN            # the app's bearer token for the kit
  read_token: keychain:orchard-payments-read   # optional: a bearer for the read routes only
  events_secret: keychain:orchard-payments-events   # signs every event the kit sends the app
provider_settings:
  webhook_secret: keychain:orchard-standin-webhook  # the provider's webhook signing secret
  decline_amount: "666"    # stand-in only: a checkout of exactly this total is declined
```

A value where a reference belongs is refused (`not env:<VAR> or keychain:<service>: it looks like a secret value`), with the same rule as [connectors](departments.md#secrets). An unknown key is refused too, so a typo doesn't pass silently as a default.

**`mode:` is the sandbox/live switch**, and only the owner flips it. `payments-kit check` prints it on its first line (`mode: sandbox (no money moves)` or `mode: live (real money)`). `mode: live` wants:

- `https` for `public_url` and `events_url` off this machine;
- every secret, the kit's and the provider's, as a `keychain:` reference: an `env:` one is refused in live ("live keys are Keychain items the owner created, separate from the sandbox ones"), since an environment variable is whatever the shell that started the kit says;
- an adapter with a live side: the stand-in has none and refuses live.

In any mode `read_token`, when set, must be a different reference from `api_token`.

## The API

Every `/v1/` route except the webhook needs `Authorization: Bearer <api_token>`; the two read routes, `GET /v1/payments/<id>` and `GET /v1/provider`, also take the `read_token`. The read token on any other route gets 403 (`this is the read token: checkouts, refunds and cancels need the api token`). Bodies are JSON, and an unknown field is refused, so no card number can be passed in.

| Route | Does |
|---|---|
| `POST /v1/checkouts` | opens a hosted checkout and answers `{id, url, payment_id, subscription_id?, expires_at}`: send the buyer to `url` |
| `GET /v1/payments/<id>` | a payment's state: `pending`, `paid`, `failed`, `refunded`, `partially_refunded` |
| `POST /v1/payments/<id>/refunds` | `{amount?, reason?, idempotency_key?}`: all that is left (no `amount`) or part. A refund over what is left is refused (422) |
| `POST /v1/subscriptions/<id>/cancel` | stops a subscription at the provider |
| `GET /v1/provider` | the adapter's name and capabilities (methods, modes, currencies, refunds, merchant of record) |
| `POST /v1/webhooks/<provider>` | the provider's webhooks (no bearer token, the provider's signature instead) |
| `GET /healthz` | `{"status":"ok"}` |

A checkout request:

```json
{
  "mode": "one_off",
  "items": [{"name": "Meal plan, 1 month", "quantity": 1, "unit_price": {"amount": 2900, "currency": "PLN"}}],
  "methods": ["blik", "card", "pay_by_link"],
  "customer": {"email": "buyer@example.com", "country": "PL"},
  "reference": "order-1042",
  "success_url": "https://app.example/paid",
  "cancel_url": "https://app.example/cart",
  "idempotency_key": "order-1042-try-1"
}
```

- **Amounts** are integers in minor units (2900 = 29.00 PLN) with an ISO 4217 currency. There is no float anywhere. One checkout has one currency.
- **Methods** have the same names for every provider: `card`, `blik`, `pay_by_link`, `bank_transfer`, `wallet` (Apple Pay, Google Pay). Leave the list out to offer every method the provider has. A method, mode or currency the provider doesn't offer is refused (422), never dropped silently.
- **Subscriptions:** `"mode": "subscription", "interval": "month" | "year"`. The provider holds the schedule, and the kit reports each renewal.
- **`reference`** is the app's own order id, and every event carries it back.

## From a department

The kit's own commands call a running kit, so an agent never builds HTTP by hand. The bearer comes from `$PAYMENTS_TOKEN`, the kit from `-url`, `$PAYMENTS_KIT_URL` or `http://127.0.0.1:8787`; the answer is the kit's JSON, and a refusal prints the kit's message and exits 1:

| Command | Token | Does |
|---|---|---|
| `payments-kit payment <payment-id>` | read | a payment's state, amount and what is refunded |
| `payments-kit provider` | read | the provider's name and capabilities |
| `payments-kit refund <payment-id> --idempotency-key <key> [--amount <minor units>] [--reason "..."]` | api | refunds all that is left, or `--amount` of it; the key is required, and the same key never refunds twice |

The Sales template ([Sales (B2C)](departments.md#sales-b2c)) writes two connectors that run them into the department's entry, commented out, beside its other examples:

- **`payments`**: `kind: tool`, `command: payments-kit`, `PAYMENTS_TOKEN: keychain:payments-read-token`, `check: payments-kit check -config payments.yaml`, `sends: false`. Lookups: `issue-cli connector exec sales/payments -- payment <payment-id>`.
- **`payments-refund`**: the same with `PAYMENTS_TOKEN: keychain:payments-api-token`, `sends: true` and `needs_approved_output: true`, so `connector exec` refuses it until the owner approved the refund issue's newest output, on top of the `owner_only` gate into publish. Only a refund's publish step uses it: `issue-cli connector exec sales/payments-refund -- refund <payment-id> --amount <approved> --idempotency-key <slug>`.

To turn them on: install the kit, write the product's `payments.yaml` with `api_token: keychain:payments-api-token` and `read_token: keychain:payments-read-token`, create both Keychain items (`security add-generic-password -s payments-api-token -a "$USER" -w`, the value typed by the owner), edit `-config` in each `check:` to the file's path, and uncomment the two entries with the [When you start charging](departments.md#when-you-start-charging) block. A command run without `$PAYMENTS_TOKEN` names the connector to run it through; a refund with the read token names `sales/payments-refund`.

The partners catalog lists the kit as the `payments` slot's default (`issue-cli department partners --slot payments`), with its licence: the kit is in this repository under its PolyForm Noncommercial licence until it moves to a repository of its own under a licence the owner picks before any live payment.

## The events

The kit POSTs each event to `events_url` as JSON, once:

| `type` | When | Carries |
|---|---|---|
| `payment.paid` | a one-off payment, or a subscription's first payment, succeeded | `payment` (with `method`), `subscription` for a subscription, `invoice` when the hook issued one |
| `payment.failed` | the payment was declined or failed | `payment` with `failure_reason` |
| `payment.refunded` | a refund went through, in part or in full | `payment` (state `partially_refunded` or `refunded`), `refund` |
| `subscription.renewed` | the provider charged the next period | the new `payment`, `subscription`, `invoice` |
| `subscription.cancelled` | the subscription stopped (the app cancelled it, the buyer did on the kit's cancel page or at the provider) | `subscription`, `confirmation` (a cancellation receipt) with the consent flow on |
| `withdrawal.requested` | the buyer confirmed a withdrawal on the kit's page (consent flow only; sent by the kit, not the provider) | `payment`, `withdrawal`, `confirmation` (the receipt) |

Every event has `id` (the provider's event id), `provider`, `occurred` and `reference`. Treat the event as the fact: grant or remove access when it arrives, not when the buyer comes back to `success_url`.

**Answer 2xx only once the event is stored.** The kit acknowledges the provider's webhook only after the app answered 2xx. If the app is down or answers 500, the provider retries and the kit sends the event again. Once the app has accepted an event, a repeated webhook (a retry, a duplicate, the same webhook ten times at once, or one arriving after a restart of the kit) is not sent again (`<state_dir>/handled.jsonl`).

### Checking the kit's signature

Each delivery has `Payments-Kit-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(events_secret, "<t>.<raw body>")>` and `Payments-Kit-Event-Id: <provider>/<id>`. Refuse a delivery whose signature doesn't match or whose `t` is more than 5 minutes from now. In TypeScript (Node):

```ts
import { createHmac, timingSafeEqual } from "node:crypto";

export function verifyKitEvent(secret: string, header: string, rawBody: string, now = Date.now()): boolean {
  const parts = Object.fromEntries(header.split(",").map((p) => p.trim().split("=", 2)));
  const t = Number(parts.t);
  if (!parts.v1 || !Number.isFinite(t) || Math.abs(now / 1000 - t) > 300) return false;
  const want = createHmac("sha256", secret).update(`${parts.t}.${rawBody}`).digest();
  const got = Buffer.from(parts.v1, "hex");
  return got.length === want.length && timingSafeEqual(got, want);
}
```

Hash the raw body as it arrived, before parsing it. In Go: `payments.Verify(secret, header, body, time.Now())`.

## Consent, withdrawal and cancel

**Placeholder wording, pending the owner's lawyer's review: no live use until then.** The kit's built-in text pack says `placeholder: true`, every page built from it shows a placeholder banner, every message carries `"placeholder": true`, and `mode: live` refuses it.

The consent flow is the consumer-law part of a checkout for digital content, built once so every project selling to consumers reuses it. Turn it on with a `consent:` block in `payments.yaml`; without one the kit behaves as before.

```yaml
consent:
  required: true              # a checkout without a consent record is refused (422)
  texts: consent-texts.yaml   # the project's reviewed pack; the built-in placeholder pack if left out
  lang: pl                    # default language: pl or en (the pack's langs)
  withdrawal_days: 14         # 14 or more
```

1. **Waiver checkbox.** `GET /v1/consent/texts?lang=pl` (the read token is enough) answers the pack's `version`, `placeholder`, `waiver_checkbox`, `withdraw_button` and `cancel_button`. The app's checkout page shows `waiver_checkbox` **unticked** and sends what the buyer chose with the checkout: `"consent": {"waiver": true, "text_version": "<version>", "lang": "pl"}`. With `required: true` a checkout without `consent`, or with another `text_version`, is refused (422, naming the texts route), and so is a `consent` sent to a kit with no `consent:` block. The kit keeps the record, the exact text shown, the answer and the time, in `<state_dir>/consents/` (0600), and the checkout answers `withdraw_url` (and `cancel_subscription_url` for a subscription): the buyer's signed links, for the app's account page. A buyer who leaves it unticked keeps the 14-day right (Directive 2011/83, art. 16(m)).
2. **Confirmation email.** On `payment.paid` for such a checkout the event carries `consent` (with `paid_at` and the `deadline`) and `confirmation: {kind: "order_confirmation", to, subject, text, lang, placeholder}`: the order, amount and date, the confirmation of the waiver (or the 14-day right with its deadline), the withdrawal link and, for a subscription, the cancel link (the confirmation on a durable medium, art. 8(7)). **The kit has no mailer: the app sends it.** A copy of every message is kept in `<state_dir>/messages/`.
3. **Withdrawal button, two steps** (Directive 2023/2673, art. 11a). `withdraw_url` opens a page with the order and one button, **"Odstąp od umowy tutaj"** ("Withdraw from contract here"). It leads to a form (name, email, the contract filled in) whose only button is **"Potwierdź odstąpienie od umowy"** ("Confirm withdrawal"). The two GET steps record nothing; only the form's POST records the statement (`<state_dir>/withdrawals/`), with its time, and sends `withdrawal.requested` once, carrying `withdrawal: {requested_at, name, email, deadline, waiver}` and the receipt the trader sends without undue delay (art. 11a(4)) as `confirmation`. A second press shows the first receipt; if the app is down the statement's time is kept and the next press delivers it. After the deadline the page says the period ended and the POST is refused (410). A buyer who ticked the waiver can still submit: `waiver: true` is on the event and the app decides, by its lawyer's rule. **The kit never refunds by itself**: the app refunds through `POST /v1/payments/<id>/refunds`, or Sales through `sales/payments-refund` and its owner gate.
4. **Subscription cancel, the same two steps.** `cancel_subscription_url` → **"Anuluj subskrypcję"** → a form → **"Potwierdź anulowanie subskrypcji"**, which cancels at the provider. Every `subscription.cancelled` for a subscription with a consent record (from this page, the app's API call or the provider) carries a `cancellation_receipt` `confirmation`.

The links are signed with a key derived from `events_secret`; a wrong or missing signature is 404, so payment ids can't be walked.

**The text pack.** The built-in one is `internal/payments/consent_texts.yaml` (Polish and English). A project copies it, has the lawyer review it, and names its copy in `consent: texts:` with a version of its own, `placeholder: false`, `reviewed_by` and `reviewed_on` (a pack with `placeholder: false` and no reviewer is refused). Every language needs every key; `payments-kit check` prints `consent: on, texts <version> (…)` with the pack's placeholder or review state. One lawyer pass on one reusable pack serves every project (report o-20261120-193a, owner choice 6).

**The law, as of 2026-11-22.** The EU withdrawal button (Directive 2023/2673, art. 11a) applies from 2026-06-19. Poland has not transposed it: the bill is UC169 (Ministry of Finance, amending the consumer-rights act; RCL 12414453), open and not yet adopted by the Council of Ministers. It went to consultation in September 2026; Poland's start date is not fixed yet, mid-2027 at the earliest. Sejm print 2799 is **not** the bill: it is the greenwashing act (Dz.U. 2026 poz. 1353), unrelated to withdrawal (corrects report o-20261121-cc40; see o-20261121-bd2c). The kit builds the two-step button now anyway: it is cheap and the law is coming. A separate "cancel subscription button" rule was not verified. **Re-check on 2027-04-01**: the RCL page and the Sejm API for a UC169 print, whether it passed, its button wording, and whether it changes any step here.

**The app's duty: the link in the purchase interface.** Under the draft the withdraw link must be shown in the app's purchase interface (the order page or account view), always and clearly visible, for the whole withdrawal period. The kit can't do that: it answers `withdraw_url`, and the app shows it. The signed link in the account and in the confirmation mail alone is not enough under the draft.

**Not built: partial withdrawal.** Partial withdrawal (chosen items of a basket) is not built. A withdrawal covers the whole contract; the form's contract field is fixed and read-only.

**The form names the receipt channel.** The withdraw form's intro says the receipt confirmation goes to the email the buyer gives (`withdraw_form_intro`), and `TestWithdrawalTwoStep` checks it. A pack the project's lawyer reviews must keep this sentence: the receipt-confirmation channel is the email.

## The invoice hook

On each `payment.paid` and `subscription.renewed` the kit calls the invoice hook (`Invoicer`) before it sends the event, and the event carries the result as `invoice`:

- `none`: issues nothing. Use it when the provider is the merchant of record (Paddle), which invoices the buyer itself.
- `record`: writes the invoice request (payment, amount, method, reference, date) as `<state_dir>/invoices/<payment id>.json`. It stands in for our own PDF invoice or a KSeF FA(3) invoice, which a later adapter of `Invoicer` adds without changing the events.

## Testing with the stand-in

The stand-in is a full provider inside the kit, with no outside party:

- **Hosted page:** a checkout's `url` is `<public_url>/standin/checkout/<id>`, a page with **Pay** (pick a method) and **Decline**. Pay sends the buyer to `success_url`, Decline to `cancel_url`.
- **Signed webhooks:** each event goes to the kit's `/v1/webhooks/standin` with `Standin-Signature` (the same `t=,v1=` form under `webhook_secret`), as a real provider's would.
- **Failure modes:** Decline gives `payment.failed`. `decline_amount:` declines every checkout of that total, for a scripted run. An over-refund is refused.
- **Controls for scripts and tests** (no token; stand-in only): `POST /standin/checkouts/<id>/pay` (form `method=blik`), `POST /standin/checkouts/<id>/decline`, `POST /standin/subscriptions/<id>/renew`, `POST /standin/subscriptions/<id>/cancel`, `GET /standin/events` (each webhook sent and whether the kit took it), `POST /standin/events/<id>/redeliver` (send one again, as a provider retrying would).
- Its state is in memory, so a restart of the kit forgets its checkouts. `handled.jsonl` is kept.

A local run end to end:

```sh
export PAYMENTS_API_TOKEN=dev-token PK_EVENTS=dev-events PK_WEBHOOK=dev-webhook
# payments.yaml: secrets as env:PAYMENTS_API_TOKEN, env:PK_EVENTS; webhook_secret: env:PK_WEBHOOK
payments-kit serve -config payments.yaml &
curl -s -H "Authorization: Bearer $PAYMENTS_API_TOKEN" -d @checkout.json http://127.0.0.1:8787/v1/checkouts
# open the url it prints, press Pay: the app's events_url gets payment.paid
```

In Go tests, `internal/payments/standin`'s tests show the whole flow on `httptest` servers (`TestKitFlowEndToEnd`), with repeated webhooks (`TestWebhookOnce`) and refused ones (`TestWebhookRefused`).

`internal/standin` is a different package: it writes executable stand-ins for tests and has nothing to do with payments.

## Adding a provider

A real provider is one package plus config. The checklist:

1. **Package** `internal/payments/<name>` implements `payments.Provider`: `Name`, `Capabilities` (only the methods, modes and currencies it really offers, and `MerchantOfRecord`), `CreateCheckout` (a **hosted** page or redirect only, never fields that take card data: PCI SAQ A; pass `IdempotencyKey` to the provider), `ParseWebhook` (verify the provider's signature **first**, against the raw body, with its timestamp tolerance; return `payments.ErrSignature` on any failure, then map to the neutral events with the provider's event id as `ID`), `Payment`, `Refund` (refuse more than what is left; pass the idempotency key), `CancelSubscription`.
2. **Register** it from `init` with `payments.Register(payments.Registration{Name, Secrets, Required, Live, New})`. Name every `provider_settings:` key that holds a key or a signing secret in `Secrets`, so a pasted value is refused. Import it in `cmd/payments-kit/main.go` and update `TestOnlyStandinRegistered`.
3. **Contract:** a test calls `paymentstest.Contract(t, setup)` with a `Driver` that pays, declines and renews in the provider's sandbox. It is skipped unless its sandbox keys are set (the owner creates the sandbox account), and it never runs in `make validate` against the network.
4. **Map every status** the provider sends to one neutral event, or ignore it on purpose with a comment. Map its method names to `card`, `blik`, `pay_by_link`, `bank_transfer` or `wallet`.
5. **Subscriptions:** use the provider's own billing (the provider holds the schedule) and report `subscription.renewed` with the new payment.
6. **Docs:** a row in this page's provider list (below), with the provider's sandbox and live settings and its webhook URL (`<public_url>/v1/webhooks/<name>`).
7. **Going live** is the owner's: a merchant account, live keys in new Keychain items (separate from the sandbox ones), `mode: live`, and the Legal word on checkout texts. A worker never does any of it.

### Providers

| Name | Kind | Status |
|---|---|---|
| `standin` | in-process stand-in, no money | built in; sandbox only |
