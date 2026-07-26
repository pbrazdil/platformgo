#!/usr/bin/env python3
"""Generate reviewed Phase 3 OpenAPI artifacts from the pinned source inventory."""

from __future__ import annotations

import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OPENAPI = ROOT / "contracts" / "openapi"

ADMIN = """
GET /admin/v1/accounts
GET /admin/v1/accounts/{accountId}
PATCH /admin/v1/accounts/{accountId}
GET /admin/v1/accounts/{accountId}/balances
GET /admin/v1/accounts/{accountId}/positions
GET /admin/v1/accounts/{accountId}/orders
GET /admin/v1/accounts/{accountId}/fills
GET /admin/v1/accounts/{accountId}/funding-history
GET /admin/v1/fills
GET /admin/v1/orders
GET /admin/v1/positions
GET /admin/v1/risk
POST /admin/v1/accounts/{accountId}/leverage
POST /admin/v1/accounts/{accountId}/flatten
POST /admin/v1/accounts/{accountId}/margin-mode
POST /admin/v1/accounts/{accountId}/oms-mode
GET /admin/v1/funding
GET /admin/v1/account-statuses
POST /admin/v1/accounts/{accountId}/status
POST /admin/v1/accounts/{accountId}/balance
POST /admin/v1/accounts
POST /admin/v1/auth/login
POST /admin/v1/auth/refresh
POST /admin/v1/me/logout
POST /admin/v1/auth/step-up
GET /admin/v1/me
POST /admin/v1/me/mfa/enroll
POST /admin/v1/me/mfa/confirm
POST /admin/v1/users
POST /admin/v1/api-keys
DELETE /admin/v1/api-keys/{id}
GET /admin/v1/diagnostics
GET /admin/v1/settings
PUT /admin/v1/settings/risk/levels
PUT /admin/v1/settings/risk/trading-mode
PUT /admin/v1/settings/risk/slippage
GET /admin/v1/permissions
GET /admin/v1/api-keys
GET /admin/v1/collections
POST /admin/v1/collections
GET /admin/v1/collections/{id}
PATCH /admin/v1/collections/{id}
DELETE /admin/v1/collections/{id}
PUT /admin/v1/collections/{id}/members
PUT /admin/v1/collections/reorder
POST /admin/v1/collections/{id}/members/{symbol}
DELETE /admin/v1/collections/{id}/members/{symbol}
GET /admin/v1/feeds/protocols
GET /admin/v1/feeds
POST /admin/v1/feeds
GET /admin/v1/feeds/discover
POST /admin/v1/feeds/import
GET /admin/v1/feeds/health
GET /admin/v1/instruments/{symbol}
PATCH /admin/v1/instruments/{symbol}
DELETE /admin/v1/instruments/{symbol}
GET /admin/v1/instruments
POST /admin/v1/instruments
POST /admin/v1/instruments/trading-mode
POST /admin/v1/instruments/enabled
GET /admin/v1/inventory
POST /admin/v1/admins
GET /admin/v1/admins
POST /admin/v1/admins/{adminId}/status
POST /admin/v1/roles
GET /admin/v1/roles
GET /admin/v1/roles/{role}/policies
POST /admin/v1/roles/{role}/policies
DELETE /admin/v1/roles/{role}/policies
POST /admin/v1/roles/{role}/parents
POST /admin/v1/admins/{adminId}/roles
GET /admin/v1/admins/{adminId}/roles
DELETE /admin/v1/admins/{adminId}/roles/{role}
GET /admin/v1/users/{userId}/sessions
DELETE /admin/v1/sessions/{sessionId}
POST /admin/v1/tenants
GET /admin/v1/tenants
GET /admin/v1/users
GET /admin/v1/users/{userId}
GET /admin/v1/users/{userId}/accounts
PATCH /admin/v1/users/{userId}
POST /admin/v1/users/{userId}/status
POST /admin/v1/users/{userId}/password-reset
POST /admin/v1/users/{userId}/unlock
GET /admin/v1/user-statuses
"""

CLIENT = """
POST /v1/auth/login
POST /v1/auth/refresh
POST /v1/me/logout
GET /v1/me
GET /v1/me/accounts
POST /v1/me/api-keys
POST /v1/me/realtime/token
GET /v1/instruments
GET /v1/collections
GET /v1/prediction-markets
GET /v1/leverage-limits/effective
POST /v1/accounts/{accountId}/orders
POST /v1/accounts/{accountId}/orders/batch
DELETE /v1/accounts/{accountId}/orders/{orderId}
PATCH /v1/accounts/{accountId}/orders/{orderId}
POST /v1/accounts/{accountId}/brackets
POST /v1/accounts/{accountId}/positions/{symbol}/close
POST /v1/accounts/{accountId}/positions/close-all
GET /v1/accounts/{accountId}/snapshot
GET /v1/accounts/{accountId}/positions
GET /v1/accounts/{accountId}/balances
GET /v1/accounts/{accountId}/margin-config/{symbol}
PUT /v1/accounts/{accountId}/margin-config/{symbol}
GET /v1/accounts/{accountId}/orders
GET /v1/accounts/{accountId}/fills
GET /v1/accounts/{accountId}/funding
"""

BROKER = """
POST /broker/v1/accounts/{accountId}/margin-mode
POST /broker/v1/accounts/{accountId}/oms-mode
POST /broker/v1/accounts/{accountId}/leverage
POST /broker/v1/accounts/{accountId}/flatten
POST /broker/v1/accounts/{accountId}/status
POST /broker/v1/accounts/{accountId}/balance
POST /broker/v1/accounts
POST /broker/v1/echo
GET /broker/v1/ping
POST /broker/v1/users
POST /broker/v1/users/{userId}/token
POST /broker/v1/realtime/token
GET /broker/v1/accounts
GET /broker/v1/accounts/{accountId}
GET /broker/v1/accounts/{accountId}/balances
GET /broker/v1/accounts/{accountId}/positions
GET /broker/v1/accounts/{accountId}/orders
GET /broker/v1/accounts/{accountId}/fills
GET /broker/v1/accounts/{accountId}/funding
"""

ACCEPTED_SURFACE: dict[tuple[str, str], dict[str, object]] = {
    ("POST", "/v1/auth/login"): {
        "statuses": [200, 400, 401, 503],
        "request": "LoginRequest",
        "success": "LoginResponse",
    },
    ("GET", "/v1/me"): {
        "statuses": [200, 401, 404, 503],
        "success": "UserProfile",
    },
    ("GET", "/v1/me/accounts"): {
        "statuses": [200, 401, 503],
        "success_array": "MyAccountView",
        "security": "bearer",
    },
    ("POST", "/v1/me/api-keys"): {
        "statuses": [201, 400, 401, 409, 429, 503],
        "idempotency": True,
        "request": "CreateAPIKeyRequest",
        "success": "APIKeyCreated",
        "security": "bearer",
        "conflict_description": "Active API-key limit reached",
    },
    ("GET", "/v1/instruments"): {
        "statuses": [200, 503],
        "success_array": "InstrumentView",
    },
    ("POST", "/v1/me/realtime/token"): {
        "statuses": [200, 401, 503],
        "success": "RealtimeToken",
    },
    ("POST", "/v1/accounts/{accountId}/orders"): {
        "statuses": [202, 400, 401, 403, 409, 503],
        "idempotency": True,
        "request": "SubmitOrderRequest",
        "success": "OrderAccepted",
    },
    ("GET", "/v1/accounts/{accountId}/orders"): {
        "statuses": [200, 400, 401, 403, 503],
        "success_array": "OrderView",
    },
    ("GET", "/v1/accounts/{accountId}/positions"): {
        "statuses": [200, 400, 401, 403, 503],
        "success_array": "PositionView",
    },
    ("GET", "/v1/accounts/{accountId}/balances"): {
        "statuses": [200, 400, 401, 403, 503],
        "success_array": "BalanceView",
    },
    ("GET", "/v1/accounts/{accountId}/funding"): {
        "statuses": [200, 400, 401, 403],
        "success": "FundingPage",
        "success_description": "Funding history, newest first",
        "pagination": True,
        "security": "bearer",
    },
    ("GET", "/broker/v1/ping"): {"statuses": [200, 401]},
    ("POST", "/broker/v1/echo"): {
        "statuses": [200, 401, 403, 409, 503],
        "idempotency": True,
        "success": "BrokerEcho",
    },
    ("POST", "/broker/v1/users"): {
        "statuses": [201, 400, 401, 403, 503],
        "request": "BrokerUserRequest",
        "success": "BrokerUserResult",
    },
    ("POST", "/broker/v1/users/{userId}/token"): {
        "statuses": [200, 400, 401, 403, 503],
        "request": "BrokerTokenRequest",
        "success": "BrokerTokenResponse",
    },
    ("POST", "/broker/v1/accounts"): {
        "statuses": [201, 400, 401, 403, 409, 503],
        "idempotency": True,
        "request": "BrokerAccountRequest",
        "success": "BrokerAccountResult",
    },
}


def response(
    status: int,
    success: str | None = None,
    success_array: str | None = None,
) -> dict[str, object]:
    descriptions = {
        200: "Success",
        201: "Created",
        202: "Accepted",
        400: "Invalid request",
        401: "Missing or invalid credentials",
        403: "Forbidden",
        404: "Not found",
        409: "Idempotency conflict",
        429: "Rate limited",
        503: "Dependency unavailable",
    }
    value: dict[str, object] = {"description": descriptions[status]}
    if status < 300 and (success is not None or success_array is not None):
        schema: dict[str, object]
        if success_array is not None:
            schema = {
                "type": "array",
                "items": {"$ref": f"#/components/schemas/{success_array}"},
            }
        else:
            schema = {"$ref": f"#/components/schemas/{success}"}
        value["content"] = {"application/json": {"schema": schema}}
    elif status >= 400:
        value["content"] = {
            "application/json": {"schema": {"$ref": "#/components/schemas/Error"}}
        }
    return value


def operations(raw: str) -> dict[str, dict[str, object]]:
    paths: dict[str, dict[str, object]] = {}
    for line in raw.strip().splitlines():
        method, path = line.split(maxsplit=1)
        operation: dict[str, object] = {
            "operationId": method.lower()
            + "_"
            + path.strip("/").replace("/", "_").replace("{", "").replace("}", ""),
            "responses": {
                "default": {
                    "description": (
                        "Frozen source route inventory only; exact behavior is "
                        "governed by accepted contract tests"
                    )
                }
            },
            "x-platformgo-contract-status": "source-route-inventory",
        }
        accepted = ACCEPTED_SURFACE.get((method, path))
        if accepted is not None:
            operation["responses"] = {
                str(status): response(
                    status,
                    accepted.get("success"),
                    accepted.get("success_array"),
                )
                for status in accepted["statuses"]
            }
            if accepted.get("success_description") is not None:
                operation["responses"]["200"]["description"] = accepted[
                    "success_description"
                ]
            if accepted.get("conflict_description") is not None:
                operation["responses"]["409"]["description"] = accepted[
                    "conflict_description"
                ]
            operation["x-platformgo-contract-status"] = "phase3-accepted-runtime"
            request_schema = accepted.get("request")
            if request_schema is not None:
                operation["requestBody"] = {
                    "required": True,
                    "content": {
                        "application/json": {
                            "schema": {
                                "$ref": f"#/components/schemas/{request_schema}"
                            }
                        }
                    },
                }
        if accepted is not None and accepted.get("idempotency") is True:
            operation["parameters"] = [
                {
                    "name": "Idempotency-Key",
                    "in": "header",
                    "required": False,
                    "schema": {"type": "string"},
                }
            ]
        if accepted is not None and accepted.get("pagination") is True:
            operation["parameters"] = [
                {
                    "name": "accountId",
                    "in": "path",
                    "required": True,
                    "schema": {"type": "string"},
                },
                {
                    "name": "limit",
                    "in": "query",
                    "required": False,
                    "schema": {"type": "integer", "minimum": 1, "maximum": 200},
                },
                {
                    "name": "cursor",
                    "in": "query",
                    "required": False,
                    "schema": {"type": "string"},
                },
                {
                    "name": "direction",
                    "in": "query",
                    "required": False,
                    "schema": {
                        "type": "string",
                        "enum": ["next", "prev", "backward"],
                    },
                },
            ]
        if accepted is not None and accepted.get("security") is not None:
            operation["security"] = [{accepted["security"]: []}]
        paths.setdefault(path, {})[method.lower()] = operation
    return paths


def schemas(client: bool) -> dict[str, object]:
    decimal = {"type": "string", "pattern": r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$"}
    return {
        "Error": {
            "type": "object",
            "required": ["code", "message"],
            "properties": {
                "code": {"type": "string"},
                "message": {"type": "string"},
                "reason": {"type": ["string", "null"]},
                "requestId": {"type": ["string", "null"]},
            },
        },
        "LoginRequest": {
            "type": "object",
            "required": ["login", "password"],
            "properties": {
                "login": {"type": "string"},
                "password": {"type": "string"},
            },
        },
        "LoginResponse": {
            "type": "object",
            "required": ["accessToken", "refreshToken"],
            "properties": {
                "accessToken": {"type": "string"},
                "refreshToken": {"type": "string"},
            },
        },
        **({
        "CreateAPIKeyRequest": {
            "type": "object",
            "required": ["name"],
            "properties": {
                "name": {
                    "type": "string",
                    "minLength": 1,
                },
                "scopes": {
                    "type": "array",
                    "items": {"type": "string"},
                    "default": [],
                },
                "ipAllowlist": {
                    "type": "array",
                    "items": {"type": "string"},
                    "default": [],
                },
                "ttlSecs": {
                    "type": ["integer", "null"],
                    "minimum": 1,
                },
                "tenantId": {"type": ["string", "null"]},
            },
        },
        "APIKeyCreated": {
            "type": "object",
            "required": ["id", "prefix", "token"],
            "properties": {
                "id": {"type": "string"},
                "prefix": {
                    "type": "string",
                    "pattern": "^[0-9a-f]{12}$",
                },
                "token": {
                    "type": "string",
                    "pattern": "^xbk_[0-9a-f]{12}\\.[0-9a-f]{48}$",
                },
            },
        },
        } if client else {}),
        "UserProfile": {
            "type": "object",
            "required": ["userId", "login", "email", "status"],
            "properties": {
                "userId": {"type": "string"},
                "login": {"type": "string"},
                "email": {"type": "string"},
                "status": {"type": "string"},
            },
        },
        **({
        "MyAccountView": {
            "type": "object",
            "required": [
                "accountId",
                "login",
                "userId",
                "baseCurrency",
                "marginMode",
                "omsMode",
                "marketVenue",
                "permittedClasses",
                "status",
                "createdAt",
            ],
            "properties": {
                "accountId": {"type": "string"},
                "login": {"type": "integer"},
                "userId": {"type": "string"},
                "baseCurrency": {"type": "string"},
                "marginMode": {
                    "type": "string",
                    "enum": ["cross", "isolated"],
                },
                "omsMode": {
                    "type": "string",
                    "enum": ["netting", "hedging"],
                },
                "marketVenue": {
                    "type": "string",
                    "enum": ["hyperliquid"],
                },
                "permittedClasses": {
                    "type": "array",
                    "items": {"type": "string", "enum": ["perps"]},
                },
                "status": {
                    "type": "string",
                    "enum": [
                        "pending",
                        "active",
                        "close_only",
                        "frozen",
                        "read_only",
                        "suspended",
                        "closed",
                    ],
                },
                "createdAt": {"type": "string", "format": "date-time"},
            },
        }}
        if client
        else {}),
        "SubmitOrderRequest": {
            "type": "object",
            "required": ["intentId", "symbol", "side", "quantity"],
            "properties": {
                "intentId": {"type": "string"},
                "symbol": {"type": "string"},
                "side": {"type": "string", "enum": ["BUY", "SELL"]},
                "type": {
                    "type": "string",
                    "enum": [
                        "MARKET",
                        "LIMIT",
                        "STOP_MARKET",
                        "STOP_LIMIT",
                        "TAKE_PROFIT_MARKET",
                        "TAKE_PROFIT_LIMIT",
                        "TRAILING_STOP_MARKET",
                    ],
                    "default": "MARKET",
                },
                "quantity": decimal,
                "price": {**decimal, "type": ["string", "null"]},
                "triggerPrice": {**decimal, "type": ["string", "null"]},
                "trailingOffset": {**decimal, "type": ["string", "null"]},
                "reduceOnly": {"type": "boolean", "default": False},
                "timeInForce": {
                    "type": ["string", "null"],
                    "enum": ["GTC", "IOC", "FOK", None],
                },
                "maxSlippageBps": {
                    "type": ["integer", "null"],
                    "minimum": 0,
                },
            },
        },
        "OrderAccepted": {
            "type": "object",
            "required": ["orderId", "intentId"],
            "properties": {
                "orderId": {"type": "string"},
                "intentId": {"type": "string"},
            },
        },
        "InstrumentView": {
            "type": "object",
            "required": [
                "symbol",
                "displayName",
                "settlementAsset",
                "priceIncrement",
                "sizeIncrement",
                "maxLeverage",
                "makerFee",
                "takerFee",
                "enabled",
            ],
            "properties": {
                "symbol": {"type": "string"},
                "displayName": {"type": "string"},
                "settlementAsset": {"type": "string"},
                "priceIncrement": decimal,
                "sizeIncrement": decimal,
                "maxLeverage": decimal,
                "makerFee": decimal,
                "takerFee": decimal,
                "enabled": {"type": "boolean"},
            },
        },
        "OrderView": {
            "type": "object",
            "required": [
                "orderId",
                "intentId",
                "symbol",
                "side",
                "type",
                "quantity",
                "status",
                "filledQuantity",
                "reduceOnly",
                "accountId",
            ],
            "properties": {
                "orderId": {"type": "string"},
                "intentId": {"type": "string"},
                "symbol": {"type": "string"},
                "side": {"type": "string"},
                "type": {"type": "string"},
                "quantity": decimal,
                "status": {"type": "string"},
                "filledQuantity": decimal,
                "limitPrice": {**decimal, "type": ["string", "null"]},
                "triggerPrice": {**decimal, "type": ["string", "null"]},
                "timeInForce": {"type": ["string", "null"]},
                "reduceOnly": {"type": "boolean"},
                "accountId": {"type": "string"},
            },
        },
        "PositionView": {
            "type": "object",
            "required": [
                "positionId",
                "symbol",
                "side",
                "quantity",
                "status",
                "accountId",
            ],
            "properties": {
                "positionId": {"type": "string"},
                "symbol": {"type": "string"},
                "side": {"type": "string"},
                "quantity": decimal,
                "status": {"type": "string"},
                "accountId": {"type": "string"},
            },
        },
        "BalanceView": {
            "type": "object",
            "required": ["currency", "total", "locked", "free", "equity"],
            "properties": {
                "currency": {"type": "string"},
                "total": decimal,
                "locked": decimal,
                "free": decimal,
                "equity": decimal,
            },
        },
        "BrokerEcho": {
            "type": "object",
            "required": ["id"],
            "properties": {"id": {"type": "string"}},
        },
        "BrokerUserRequest": {
            "type": "object",
            "required": ["login", "email"],
            "properties": {
                "login": {"type": "string"},
                "email": {"type": "string"},
            },
        },
        "BrokerUserResult": {
            "type": "object",
            "required": ["id", "created"],
            "properties": {
                "id": {"type": "string"},
                "created": {"type": "boolean"},
            },
        },
        "BrokerTokenRequest": {
            "type": "object",
            "properties": {
                "ttlSecs": {"type": ["integer", "null"], "minimum": 1}
            },
        },
        "BrokerTokenResponse": {
            "type": "object",
            "required": ["accessToken", "expiresInSecs"],
            "properties": {
                "accessToken": {"type": "string"},
                "expiresInSecs": {"type": "integer", "minimum": 1},
            },
        },
        "BrokerAccountRequest": {
            "type": "object",
            "required": ["userId"],
            "properties": {
                "userId": {"type": "string"},
                "baseCurrency": {"type": ["string", "null"]},
                "venue": {"type": ["string", "null"]},
            },
        },
        "BrokerAccountResult": {
            "type": "object",
            "required": [
                "id",
                "login",
                "userId",
                "baseCurrency",
                "marketVenue",
                "permittedClasses",
                "createdAt",
            ],
            "properties": {
                "id": {"type": "string"},
                "login": {"type": "integer"},
                "userId": {"type": "string"},
                "baseCurrency": {"type": "string"},
                "marketVenue": {"type": "string"},
                "permittedClasses": {
                    "type": "array",
                    "items": {"type": "string"},
                },
                "createdAt": {"type": "string", "format": "date-time"},
            },
        },
        "FundingView": (
            {
                "type": "object",
                "required": [
                    "fundingId",
                    "symbol",
                    "positionId",
                    "positionSignedQty",
                    "oraclePrice",
                    "fundingRate",
                    "fundingAmount",
                    "currency",
                    "fundingTime",
                ],
                "properties": {
                    "fundingId": {"type": "string"},
                    "symbol": {"type": "string"},
                    "positionId": {"type": "string"},
                    "positionSignedQty": decimal,
                    "oraclePrice": decimal,
                    "fundingRate": decimal,
                    "fundingAmount": decimal,
                    "currency": {"type": "string"},
                    "fundingTime": {"type": "string", "format": "date-time"},
                    "accountLogin": {"type": "integer"},
                },
            }
            if client
            else {
                "type": "object",
                "properties": {
                    "amount": decimal,
                    "rate": decimal,
                },
            }
        ),
        **(
            {
                "FundingPage": {
                    "type": "object",
                    "required": ["items"],
                    "properties": {
                        "items": {
                            "type": "array",
                            "items": {"$ref": "#/components/schemas/FundingView"},
                        },
                        "nextCursor": {"type": "string"},
                        "prevCursor": {"type": "string"},
                        "total": {"type": "integer"},
                    },
                }
            }
            if client
            else {}
        ),
        "RealtimeToken": {
            "type": "object",
            "required": ["token", "channels"],
            "properties": {
                "token": {"type": "string"},
                "channels": {"type": "array", "items": {"type": "string"}},
            },
        },
    }


def document(title: str, raw: str, security: str) -> dict[str, object]:
    return {
        "openapi": "3.1.0",
        "info": {
            "title": title,
            "version": "0.1.0",
            "x-platform-source-revision": "50141367492be46ebf5623f6191a14b94af2f2bd",
        },
        "x-platformgo-artifact-kind": (
            "pinned-source-route-inventory-with-phase3-accepted-runtime-surface"
        ),
        "paths": operations(raw),
        "components": {
            "securitySchemes": {
                security: (
                    {"type": "apiKey", "in": "header", "name": "X-API-Key"}
                    if security == "apiKey"
                    else {"type": "http", "scheme": "bearer"}
                )
            },
            "schemas": schemas(raw == CLIENT),
        },
    }


def main() -> None:
    OPENAPI.mkdir(parents=True, exist_ok=True)
    artifacts = {
        "admin-v1.json": document("uzo Admin API", ADMIN, "bearer"),
        "client-v1.json": document("uzo Client API", CLIENT, "bearer"),
        "broker-v1.json": document("uzo Broker API", BROKER, "apiKey"),
    }
    for name, value in artifacts.items():
        (OPENAPI / name).write_text(
            json.dumps(value, indent=2, sort_keys=True) + "\n",
            encoding="utf-8",
        )


if __name__ == "__main__":
    main()
