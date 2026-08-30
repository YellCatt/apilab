// Package router 定义了 HTTP 路由注册逻辑，将 URL 路径映射到对应的 Controller 方法。
package router

import (
	"net/http"

	"github.com/YellCatt/apilab/controller"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// NewRouter 创建并配置 HTTP 请求路由器，注册所有 API 路由及 Swagger 文档。
func NewRouter(userController *controller.UserController, statusController *controller.StatusController, traceController *controller.TraceController) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","message":"Service is running"}`))
	})

	mux.HandleFunc("GET /status", statusController.GetStatus)

	mux.HandleFunc("POST /api/users", userController.CreateUser)
	mux.HandleFunc("GET /api/users", userController.GetAllUsers)
	mux.HandleFunc("GET /api/users/{id}", userController.GetUserByID)
	mux.HandleFunc("PUT /api/users/{id}", userController.UpdateUser)
	mux.HandleFunc("DELETE /api/users/{id}", userController.DeleteUser)

	mux.HandleFunc("POST /api/traces/report", traceController.Report)

	mux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	mux.HandleFunc("GET /swagger/doc.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(swaggerDoc))
	})

	return mux
}

// swaggerDoc 内嵌的 Swagger 2.0 文档定义，用于 Swagger UI 展示 API 文档。
const swaggerDoc = `{
  "swagger": "2.0",
  "info": {
    "description": "apilab - Go API Service",
    "title": "apilab",
    "contact": {},
    "version": "1.0"
  },
  "host": "localhost:8084",
  "basePath": "/",
  "paths": {
    "/api/traces/report": {
      "post": {
        "description": "Report trace events, buffered and batch-forwarded to the collector",
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["traces"],
        "summary": "Report trace events",
        "parameters": [
          {
            "description": "Trace events payload",
            "name": "body",
            "in": "body",
            "required": true,
            "schema": { "$ref": "#/definitions/model.TraceReportRequest" }
          }
        ],
        "responses": {
          "200": {
            "description": "Success",
            "schema": { "$ref": "#/definitions/model.TraceReportResponse" }
          },
          "400": { "description": "Bad Request" }
        }
      }
    },
    "/api/users": {
      "get": {
        "description": "Get all users",
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["users"],
        "summary": "Get all users",
        "responses": {
          "200": {
            "description": "Success",
            "schema": {
              "type": "array",
              "items": { "$ref": "#/definitions/model.User" }
            }
          }
        }
      },
      "post": {
        "description": "Create a new user",
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["users"],
        "summary": "Create user",
        "parameters": [
          {
            "description": "User object",
            "name": "user",
            "in": "body",
            "required": true,
            "schema": { "$ref": "#/definitions/model.CreateUserRequest" }
          }
        ],
        "responses": {
          "201": {
            "description": "Created",
            "schema": { "$ref": "#/definitions/model.User" }
          },
          "400": { "description": "Bad Request" }
        }
      }
    },
    "/api/users/{id}": {
      "get": {
        "description": "Get user by ID",
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["users"],
        "summary": "Get user",
        "parameters": [
          {
            "type": "integer",
            "description": "User ID",
            "name": "id",
            "in": "path",
            "required": true
          }
        ],
        "responses": {
          "200": {
            "description": "Success",
            "schema": { "$ref": "#/definitions/model.User" }
          },
          "404": { "description": "Not Found" }
        }
      },
      "put": {
        "description": "Update user",
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["users"],
        "summary": "Update user",
        "parameters": [
          {
            "type": "integer",
            "description": "User ID",
            "name": "id",
            "in": "path",
            "required": true
          },
          {
            "description": "User object",
            "name": "user",
            "in": "body",
            "required": true,
            "schema": { "$ref": "#/definitions/model.UpdateUserRequest" }
          }
        ],
        "responses": {
          "200": {
            "description": "Success",
            "schema": { "$ref": "#/definitions/model.User" }
          },
          "404": { "description": "Not Found" }
        }
      },
      "delete": {
        "description": "Delete user",
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["users"],
        "summary": "Delete user",
        "parameters": [
          {
            "type": "integer",
            "description": "User ID",
            "name": "id",
            "in": "path",
            "required": true
          }
        ],
        "responses": {
          "204": { "description": "No Content" },
          "404": { "description": "Not Found" }
        }
      }
    }
  },
  "definitions": {
    "model.TraceReportRequest": {
      "type": "object",
      "properties": {
        "events": {
          "type": "array",
          "items": { "$ref": "#/definitions/model.TraceEvent" }
        }
      }
    },
    "model.TraceEvent": {
      "type": "object",
      "properties": {
        "trace_id": { "type": "string" },
        "span_id": { "type": "string" },
        "parent_span_id": { "type": "string" },
        "timestamp": { "type": "string", "format": "date-time" },
        "level": { "type": "string" },
        "module": { "type": "string" },
        "event": { "type": "string" },
        "message": { "type": "string" },
        "params": { "type": "object" },
        "error_message": { "type": "string" }
      }
    },
    "model.TraceReportResponse": {
      "type": "object",
      "properties": {
        "code": { "type": "integer" },
        "message": { "type": "string" },
        "count": { "type": "integer" }
      }
    },
    "model.CreateUserRequest": {
      "type": "object",
      "properties": {
        "name": { "type": "string" },
        "age": { "type": "integer" }
      },
      "required": ["name", "age"]
    },
    "model.UpdateUserRequest": {
      "type": "object",
      "properties": {
        "name": { "type": "string" },
        "age": { "type": "integer" }
      }
    },
    "model.User": {
      "type": "object",
      "properties": {
        "ID": { "type": "integer" },
        "CreatedAt": { "type": "string", "format": "date-time" },
        "UpdatedAt": { "type": "string", "format": "date-time" },
        "DeletedAt": { "type": "string", "format": "date-time" },
        "name": { "type": "string" },
        "age": { "type": "integer" }
      }
    }
  }
}`
