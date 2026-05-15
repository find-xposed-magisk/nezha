package model

import (
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCommonHasPermission(t *testing.T) {
	resource := &Common{ID: 10, UserID: 100}

	t.Run("unauthenticated denied", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		if resource.HasPermission(ctx) {
			t.Fatal("expected unauthenticated request to be denied")
		}
	})

	t.Run("owner allowed", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
		if !resource.HasPermission(ctx) {
			t.Fatal("expected owner to be allowed")
		}
	})

	t.Run("foreign member denied", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 200}, Role: RoleMember})
		if resource.HasPermission(ctx) {
			t.Fatal("expected non-owner member to be denied")
		}
	})

	t.Run("admin allowed", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 1}, Role: RoleAdmin})
		if !resource.HasPermission(ctx) {
			t.Fatal("expected admin to be allowed")
		}
	})
}

func TestSearchByID(t *testing.T) {
	t.Run("WithoutPriorityList", func(t *testing.T) {
		list, exp := []*DDNSProfile{
			{Common: Common{ID: 1}},
			{Common: Common{ID: 2}},
			{Common: Common{ID: 3}},
			{Common: Common{ID: 4}},
			{Common: Common{ID: 5}},
		}, []*DDNSProfile{
			{Common: Common{ID: 4}},
			{Common: Common{ID: 1}},
			{Common: Common{ID: 3}},
		}

		searchList := slices.Values([]string{"4", "1", "3"})
		filtered := SearchByID(searchList, list)
		if !reflect.DeepEqual(filtered, exp) {
			t.Fatalf("expected %v, but got %v", exp, filtered)
		}
	})

	t.Run("WithPriorityTest", func(t *testing.T) {
		list, exp := []*Server{
			{Common: Common{ID: 5}, DisplayIndex: 2},
			{Common: Common{ID: 4}, DisplayIndex: 1},
			{Common: Common{ID: 1}},
			{Common: Common{ID: 2}},
			{Common: Common{ID: 3}},
		}, []*Server{
			{Common: Common{ID: 4}, DisplayIndex: 1},
			{Common: Common{ID: 5}, DisplayIndex: 2},
			{Common: Common{ID: 3}},
		}

		searchList := slices.Values([]string{"3", "4", "5"})
		filtered := SearchByID(searchList, list)
		if !reflect.DeepEqual(filtered, exp) {
			t.Fatalf("expected %v, but got %v", exp, filtered)
		}
	})
}
