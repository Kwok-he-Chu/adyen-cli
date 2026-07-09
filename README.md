# Adyen CLI

This example generates Go client packages for every versioned Adyen OpenAPI spec in `adyen-openapi`, excluding the notification specs, and provides a generic CLI for listing and sending API requests.

## Generate

```sh
go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@v2.2.0

cd src/

go generate ./adyen-cli
```

## Run

```sh
cd src/

go run ./adyen-cli specs

go run ./adyen-cli request --spec ./adyen-openapi/json/CheckoutService-v72 --method POST --path /payments --api-key "$ADYEN_API_KEY" --body '{"amount":{"currency":"USD","value":1000},"reference":"Your order number","paymentMethod":{"type":"scheme","number":"test_4111111111111111","expiryMonth":"test_03","expiryYear":"test_2030","holderName":"John Smith","cvc":"test_737"},"returnUrl":"https://your-company.example.com/...","merchantAccount":"$ADYEN_MERCHANT_ACCOUNT"}'
```

If you export `ADYEN_API_KEY`, you can omit `--api-key`. To pass it from shell:

```sh
cd src/

export ADYEN_API_KEY=...

go run ./adyen-cli request --spec ./adyen-openapi/json/CheckoutService-v72 --method POST --path /payments --api-key "$ADYEN_API_KEY" --body '{"amount":{"currency":"USD","value":1000},"reference":"Your order number","paymentMethod":{"type":"scheme","number":"test_4111111111111111","expiryMonth":"test_03","expiryYear":"test_2030","holderName":"John Smith","cvc":"test_737"},"returnUrl":"https://your-company.example.com/...","merchantAccount":"$ADYEN_MERCHANT_ACCOUNT"}'
```

---

## Adyen Integration Setup — Individual Commands

The following commands set up a complete Adyen Checkout integration using the **Management API v3**
(`https://management-test.adyen.com/v3`).  All commands use `ManagementService-v3.yaml` as the spec and require a **Management API key** (`ADYEN_MANAGEMENT_API_KEY`).

> **Company-level vs merchant-level credentials**
> The Management API exposes two parallel sets of endpoints: `/companies/{companyId}/apiCredentials` (company-level) and `/merchants/{merchantAccount}/apiCredentials` (merchant-level). A company-level key manages the company and all child merchants; a merchant-level key is scoped to one merchant account. The commands below all target merchant-level endpoints, which is the correct scope for a per-merchant Checkout integration.

Prerequisites:

```sh
export ADYEN_MANAGEMENT_API_KEY=<your-management-api-key>
ADYEN_MERCHANT_ACCOUNT=<YOUR_ADYEN_MERCHANT_ACCOUNT>
```

### 1. `create-merchant-api-credential`

Creates a merchant-level API credential with the Checkout webservice role.  
Save `id` (credential ID) and `apiKey` — **`apiKey` is shown only once**.

```sh
go run ./adyen-cli request \
  --spec ManagementService-v3.yaml \
  --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/apiCredentials" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --body '{"roles":["Checkout webservice role"],"description":"AgentX integration credential"}'
```

```sh
CREDENTIAL_ID=<id from response>
ADYEN_API_KEY=<apiKey from response>   # write to .env immediately — not re-fetchable
```

### 2. `generate-merchant-client-key`

Generates a client key for the Drop-in SDK from an existing merchant credential.

```sh
go run ./adyen-cli request \
  --spec ManagementService-v3.yaml \
  --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/apiCredentials/$CREDENTIAL_ID/generateClientKey" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY"
```

```sh
ADYEN_CLIENT_KEY=<clientKey from response>
```

### 3. `register-merchant-allowed-origin`

Registers an allowed origin on the merchant credential so the Drop-in SDK can load from that domain.

```sh
go run ./adyen-cli request \
  --spec ManagementService-v3.yaml \
  --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/apiCredentials/$CREDENTIAL_ID/allowedOrigins" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --body '{"domain":"http://localhost:3000"}'
```

### 4. `create-merchant-webhook`

Creates a standard webhook on the merchant account that receives all event codes (including `AUTHORISATION` and `CHARGEBACK`) by default.  Do **not** add `filterMerchantAccountType`, `filterMerchantAccounts`, or `additionalSettings.hmacSignatureV2` — these cause 400/422 errors on the merchant endpoint.

```sh
WEBHOOK_URL=https://your-tunnel.ngrok.io/api/webhook

go run ./adyen-cli request \
  --spec ManagementService-v3.yaml \
  --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/webhooks" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --body "{\"type\":\"standard\",\"url\":\"$WEBHOOK_URL\",\"active\":true,\"communicationFormat\":\"json\"}"
```

```sh
WEBHOOK_ID=<id from response>
```

### 5. `generate-merchant-webhook-hmac`

Generates the HMAC signing key for the webhook.  
**Wait at least 1 second after creating the webhook** before calling this — the Management API has a propagation delay.

```sh
sleep 1

go run ./adyen-cli request \
  --spec ManagementService-v3.yaml \
  --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/webhooks/$WEBHOOK_ID/generateHmac" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY"
```

```sh
ADYEN_HMAC_KEY=<hmacKey from response>
```

### 6. `list-merchant-payment-methods`

Lists the configured payment methods for the merchant account.  Filter for `"enabled": true`.  Card-network types (`visa`, `mc`, `amex`, `maestro`, etc.) map to `"scheme"` in Checkout Drop-in; everything else (`ideal`, `klarna`, `paypal`, etc.) is a separate method type.

Requires the **Management API — Payment methods read** role on `ADYEN_MANAGEMENT_API_KEY`.

```sh
go run ./adyen-cli request \
  --spec ManagementService-v3.yaml \
  --method GET \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/paymentMethodSettings" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY"
```

---

### Full chain (copy-paste sequence)

```sh
cd src/

export ADYEN_MANAGEMENT_API_KEY=<your-management-api-key>
ADYEN_MERCHANT_ACCOUNT=<YourMerchantAccountID>
WEBHOOK_URL=https://your-tunnel.ngrok.io/api/webhook

# 1. create-merchant-api-credential
CRED=$(go run ./adyen-cli request \
  --spec ManagementService-v3.yaml --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/apiCredentials" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --body '{"roles":["Checkout webservice role"],"description":"AgentX integration credential"}')
echo "$CRED"
CREDENTIAL_ID=$(echo "$CRED" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
ADYEN_API_KEY=$(echo "$CRED" | python3 -c "import sys,json; print(json.load(sys.stdin)['apiKey'])")

# 2. generate-merchant-client-key
CLIENT=$(go run ./adyen-cli request \
  --spec ManagementService-v3.yaml --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/apiCredentials/$CREDENTIAL_ID/generateClientKey" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY")
ADYEN_CLIENT_KEY=$(echo "$CLIENT" | python3 -c "import sys,json; print(json.load(sys.stdin)['clientKey'])")

# 3. register-merchant-allowed-origin
go run ./adyen-cli request \
  --spec ManagementService-v3.yaml --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/apiCredentials/$CREDENTIAL_ID/allowedOrigins" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --body '{"domain":"http://localhost:3000"}'

# 4. create-merchant-webhook
WH=$(go run ./adyen-cli request \
  --spec ManagementService-v3.yaml --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/webhooks" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --body "{\"type\":\"standard\",\"url\":\"$WEBHOOK_URL\",\"active\":true,\"communicationFormat\":\"json\"}")
WEBHOOK_ID=$(echo "$WH" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

# 5. generate-merchant-webhook-hmac (wait 1s for propagation)
sleep 1
HMAC=$(go run ./adyen-cli request \
  --spec ManagementService-v3.yaml --method POST \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/webhooks/$WEBHOOK_ID/generateHmac" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY")
ADYEN_HMAC_KEY=$(echo "$HMAC" | python3 -c "import sys,json; print(json.load(sys.stdin)['hmacKey'])")

# 6. list-merchant-payment-methods
go run ./adyen-cli request \
  --spec ManagementService-v3.yaml --method GET \
  --path "/merchants/$ADYEN_MERCHANT_ACCOUNT/paymentMethodSettings" \
  --api-key "$ADYEN_MANAGEMENT_API_KEY"

# Print .env block
cat <<EOF
ADYEN_API_KEY=$ADYEN_API_KEY
ADYEN_CLIENT_KEY=$ADYEN_CLIENT_KEY
ADYEN_HMAC_KEY=$ADYEN_HMAC_KEY
ADYEN_MERCHANT_ACCOUNT=$ADYEN_MERCHANT_ACCOUNT
EOF
```

---

## One-shot: `setup-integration`

The `setup-integration` command runs all six steps above in a single invocation and prints the resulting `.env` values.

```sh
cd src/

go run ./adyen-cli setup-integration \
  --management-api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --merchant-account  AdyenAccount815ECOM \
  --webhook-url  https://adyen.com \
  --allowed-origin       http://localhost:3000
```

Optional flags:

| Flag | Default | Description |
|---|---|---|
| `--management-api-key` | `$ADYEN_MANAGEMENT_API_KEY` env var | Management API key |
| `--merchant-account` | _(required)_ | Merchant account ID |
| `--webhook-url` | _(required)_ | Webhook endpoint URL |
| `--allowed-origin` | `http://localhost:3000` | Allowed origin for Drop-in |
| `--base-url` | `https://management-test.adyen.com/v3` | Management API base URL |
| `--description` | `AgentX integration credential` | Credential description |

On success the command prints a ready-to-use `.env` block:

```
ADYEN_API_KEY=AQE...
ADYEN_CLIENT_KEY=test_...
ADYEN_HMAC_KEY=ABC123...
ADYEN_MERCHANT_ACCOUNT=YOUR_ADYEN_MERCHANT_ACCOUNT
```









adyen-cli setup-integration \
  --management-api-key "$ADYEN_MANAGEMENT_API_KEY" \
  --merchant-account  "$ADYEN_MERCHANT_ACCOUNT" \
  --webhook-url  https://adyen.com \
  --allowed-origin       http://localhost:3000

adyen-cli  scaffold spring-boot-java   // Empty template


1. step 1 // sessions

// CodeCrafter
adyen-cli lookup-snippet java --url https://checkout-test.adyen.com/v72/sessions
{
  "merchantAccount": "{ADYEN_MERCHANT_ACCOUNT}",
  "amount": { "value": 10000, "currency": "EUR" },
  "countryCode": "NL",
  "channel": "Web",
  "returnUrl": "http://localhost:3000/checkout.html",
  "reference": "{merchantReference}",
  "shopperEmail": "{shopper's email address}",
  "shopperIP": "{shopper's IP address, e.g. req.ip in Express}",
  "shopperLocale": "nl-NL",
  "allowedPaymentMethods": ["scheme"]
}
