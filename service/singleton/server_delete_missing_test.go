package singleton

import (
	"testing"

	"github.com/nezhahq/nezha/model"
)

func TestServerClassDeleteMissingIDNoPanic(t *testing.T) {
	c := &ServerClass{
		class: class[uint64, *model.Server]{
			list: map[uint64]*model.Server{
				1: {Common: model.Common{ID: 1}, UUID: "uuid-1"},
			},
		},
		uuidToID: map[string]uint64{"uuid-1": 1},
	}

	c.Delete([]uint64{999999})

	if _, ok := c.list[1]; !ok {
		t.Fatalf("existing server 1 must remain after deleting a non-existent id")
	}

	c.Delete([]uint64{1, 424242})
	if _, ok := c.list[1]; ok {
		t.Fatalf("server 1 should be removed")
	}
	if _, ok := c.uuidToID["uuid-1"]; ok {
		t.Fatalf("uuid mapping for server 1 should be removed")
	}
}
