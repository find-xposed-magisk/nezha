//go:build !agentcompat

package controller

import "github.com/gin-gonic/gin"

func registerAgentcompatRoutes(*gin.Engine) {}
