package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedAudit(t *testing.T, rows []model.CommandExecution) {
	t.Helper()
	for i := range rows {
		if err := database.GetDB().Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed audit row: %v", err)
		}
	}
}

func TestExecHistoryFiltersAndPaginates(t *testing.T) {
	setupConflictDB(t)
	svc := &NodeService{}
	seedAudit(t, []model.CommandExecution{
		{NodeId: 1, NodeName: "a", Username: "admin", Command: "uptime", Status: "success"},
		{NodeId: 1, NodeName: "a", Username: "admin", Command: "df", Status: "failed"},
		{NodeId: 2, NodeName: "b", Username: "ops", Command: "uptime", Status: "success"},
	})

	all, err := svc.ExecHistory(ExecHistoryParams{})
	if err != nil {
		t.Fatalf("ExecHistory: %v", err)
	}
	if all.Total != 3 || len(all.Items) != 3 {
		t.Fatalf("unfiltered total=%d items=%d, want 3/3", all.Total, len(all.Items))
	}
	// Newest first: the last inserted row (node 2) must lead.
	if all.Items[0].NodeId != 2 {
		t.Fatalf("first item nodeId=%d, want 2 (newest first)", all.Items[0].NodeId)
	}

	byNode, err := svc.ExecHistory(ExecHistoryParams{NodeId: 1})
	if err != nil {
		t.Fatalf("ExecHistory by node: %v", err)
	}
	if byNode.Total != 2 {
		t.Fatalf("node filter total=%d, want 2", byNode.Total)
	}

	byStatus, err := svc.ExecHistory(ExecHistoryParams{Status: "failed"})
	if err != nil {
		t.Fatalf("ExecHistory by status: %v", err)
	}
	if byStatus.Total != 1 || byStatus.Items[0].Command != "df" {
		t.Fatalf("status filter = %+v, want the single failed 'df' row", byStatus.Items)
	}

	byUser, err := svc.ExecHistory(ExecHistoryParams{Username: "ops"})
	if err != nil {
		t.Fatalf("ExecHistory by user: %v", err)
	}
	if byUser.Total != 1 || byUser.Items[0].NodeId != 2 {
		t.Fatalf("user filter = %+v, want the single ops row on node 2", byUser.Items)
	}
}

func TestExecHistoryPageSizeClamp(t *testing.T) {
	setupConflictDB(t)
	svc := &NodeService{}
	rows := make([]model.CommandExecution, 5)
	for i := range rows {
		rows[i] = model.CommandExecution{NodeId: 1, NodeName: "a", Username: "admin", Command: "x", Status: "success"}
	}
	seedAudit(t, rows)

	page1, err := svc.ExecHistory(ExecHistoryParams{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("ExecHistory: %v", err)
	}
	if len(page1.Items) != 2 || page1.Total != 5 {
		t.Fatalf("page1 items=%d total=%d, want 2/5", len(page1.Items), page1.Total)
	}

	// Oversized page size is clamped, not honored verbatim.
	big, err := svc.ExecHistory(ExecHistoryParams{PageSize: 100000})
	if err != nil {
		t.Fatalf("ExecHistory: %v", err)
	}
	if big.PageSize != execHistoryMaxPageSize {
		t.Fatalf("pageSize=%d, want clamp to %d", big.PageSize, execHistoryMaxPageSize)
	}
}

func TestPruneExecHistory(t *testing.T) {
	setupConflictDB(t)
	svc := &NodeService{}
	oldTs := time.Now().AddDate(0, 0, -100).UnixMilli()
	newTs := time.Now().UnixMilli()
	seedAudit(t, []model.CommandExecution{
		{NodeId: 1, NodeName: "a", Command: "old", Status: "success", CreatedAt: oldTs},
		{NodeId: 1, NodeName: "a", Command: "new", Status: "success", CreatedAt: newTs},
	})

	removed, err := svc.PruneExecHistory(30)
	if err != nil {
		t.Fatalf("PruneExecHistory: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want 1 (only the 100-day-old row)", removed)
	}
	remaining, _ := svc.ExecHistory(ExecHistoryParams{})
	if remaining.Total != 1 || remaining.Items[0].Command != "new" {
		t.Fatalf("after prune = %+v, want only the recent row", remaining.Items)
	}
}

func TestPruneExecHistoryRejectsNonPositive(t *testing.T) {
	setupConflictDB(t)
	svc := &NodeService{}
	if _, err := svc.PruneExecHistory(0); err == nil {
		t.Fatal("PruneExecHistory(0) succeeded, want it rejected to avoid wiping the whole log")
	}
	if _, err := svc.PruneExecHistory(-5); err == nil {
		t.Fatal("PruneExecHistory(-5) succeeded, want it rejected")
	}
}
