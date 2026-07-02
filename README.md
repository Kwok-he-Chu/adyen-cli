# Adyen CLI

This example generates Go client packages for every versioned Adyen OpenAPI spec in `adyen-openapi`, excluding the notification specs, and provides a generic CLI for listing and calling them.

## Generate

```sh
go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@v2.2.0

cd src/

go generate ./adyen-cli
```

## Run

```sh
cd src/

//go run ./adyen-cli specs

go run ./adyen-cli request --spec ./adyen-openapi/json/CheckoutService-v72 --method POST --path /payments --api-key "$ADYEN_API_KEY" --body '{"amount":{"currency":"USD","value":1000},"reference":"Your order number","paymentMethod":{"type":"scheme","number":"4111111111111111","expiryMonth":"03","expiryYear":"2030","holderName":"John Smith","cvc":"737"},"returnUrl":"https://your-company.example.com/...","merchantAccount":"$ADYEN_MERCHANT_ACCOUNT"}'
```

If you export `ADYEN_API_KEY`, you can omit `--api-key` entirely. To pass it explicitly from the shell, use:

```sh
cd src/

export ADYEN_API_KEY=...

go run ./adyen-cli request --spec ./adyen-openapi/json/CheckoutService-v72 --method POST --path /payments --api-key "$ADYEN_API_KEY" --body '{"amount":{"currency":"USD","value":1000},"reference":"Your order number","paymentMethod":{"type":"scheme","number":"4111111111111111","expiryMonth":"03","expiryYear":"2030","holderName":"John Smith","cvc":"737"},"returnUrl":"https://your-company.example.com/...","merchantAccount":"$ADYEN_MERCHANT_ACCOUNT"}'
```
