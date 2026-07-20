//go:build agentcompat && !linux

package controller

import "github.com/gin-gonic/gin"

func registerAgentcompatSQLiteHoldRoutes(*gin.Engine, gin.HandlerFunc) {}
