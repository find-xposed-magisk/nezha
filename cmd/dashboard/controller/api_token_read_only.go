package controller

import "github.com/gin-gonic/gin"

func suppressAPITokenAuthWrites(c *gin.Context) { c.Set(apiTokenReadOnlyCtxKey, true) }

func apiTokenAuthReadOnly(c *gin.Context) bool {
	value, ok := c.Get(apiTokenReadOnlyCtxKey)
	return ok && value == true
}

func apiTokenLastUsedOnce(c *gin.Context) bool {
	value, seen := c.Get(apiTokenLastUsedCtxKey)
	if seen && value == true {
		return false
	}
	c.Set(apiTokenLastUsedCtxKey, true)
	return true
}
