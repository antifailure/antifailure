package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/crossstore"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/masking"
)

// The fourth question, and the reason it has three verdicts rather than a
// percentage.
//
// A model reading a number has to decide what it means, and the number that
// needs deciding about is the one produced by comparing nothing: zero of zero
// renders as a percentage exactly the way a real failure does. So the verdict
// is the answer and the number is the evidence for it.

func crossStoreReaders(res *env.CrossStoreResult, err error) maskingReaders {
	return maskingReaders{
		CrossStore: func(context.Context) (*env.CrossStoreResult, error) { return res, err },
	}
}

func bothStores(checked, identical int) *env.CrossStoreResult {
	return &env.CrossStoreResult{
		Declared: []string{"primary", "events"},
		Report: crossstore.Report{
			Read: []string{"primary", "events"}, Tables: 12, Columns: 96,
			Cross: masking.CrossStoreReport{
				Stores:  []string{"primary", "events"},
				Checked: checked, Identical: identical,
			},
		},
	}
}

func TestMaskCrossStore_AgreementIsAPassWithTheNumberUnderIt(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, crossStoreReaders(bothStores(4, 4), nil), args("question", "cross_store"))
	require.Nil(t, fault)

	doc := out.(*maskingCrossStoreDoc)
	require.Equal(t, VerdictPass, doc.Verdict)
	require.Equal(t, 4, doc.Identical)
	require.NotNil(t, doc.Percent)
	require.Equal(t, 100.0, *doc.Percent)
	require.Equal(t, 0, doc.RowsRead,
		"a caller deciding whether this is safe to point at production reads this field "+
			"rather than trusting the description")
	require.Equal(t, 96, doc.Columns)
}

func TestMaskCrossStore_ADisagreementIsAFailAndNamesThePair(t *testing.T) {
	t.Parallel()
	res := bothStores(4, 3)
	res.Report.Cross.Pairs = []masking.JoinPair{{
		Key:    "email",
		A:      masking.JoinColumn{Store: "primary", Table: "public.person", Column: "email"},
		B:      masking.JoinColumn{Store: "events", Table: "af.events", Column: "email"},
		Reason: "one identity becomes two people",
	}}
	out, fault := callMask(t, crossStoreReaders(res, nil), args("question", "cross_store"))
	require.Nil(t, fault)

	doc := out.(*maskingCrossStoreDoc)
	require.Equal(t, VerdictFail, doc.Verdict)
	require.Equal(t, 1, doc.MismatchesTotal)
	require.Equal(t, "events.af.events.email", doc.Mismatches[0].B,
		"the store is part of the name, because two stores can hold a table with one "+
			"name and a message that dropped it would name a column twice")
}

// The one that matters. Two stores that share no identifier is zero of zero,
// and a percentage over zero pairs is not a pass.
func TestMaskCrossStore_NothingComparedIsInconclusiveRatherThanAPass(t *testing.T) {
	t.Parallel()
	out, fault := callMask(t, crossStoreReaders(bothStores(0, 0), nil), args("question", "cross_store"))
	require.Nil(t, fault)

	doc := out.(*maskingCrossStoreDoc)
	require.Equal(t, VerdictInconclusive, doc.Verdict)
	require.Nil(t, doc.Percent,
		"zero percent and no comparison are opposite facts, and a field that renders "+
			"0.0 for both is unreadable")
}

// One store read is the other way of comparing nothing, and it reaches the
// verdict through a different branch.
func TestMaskCrossStore_OneStoreReadIsInconclusive(t *testing.T) {
	t.Parallel()
	res := bothStores(0, 0)
	res.Report.Read = []string{"primary"}
	res.Report.Unread = []crossstore.Unread{
		{Store: "events", Engine: "clickhouse", Why: "connection refused"},
	}
	res.WithoutSource = []string{"cache"}

	out, fault := callMask(t, crossStoreReaders(res, nil), args("question", "cross_store"))
	require.Nil(t, fault)

	doc := out.(*maskingCrossStoreDoc)
	require.Equal(t, VerdictInconclusive, doc.Verdict)
	require.Len(t, doc.Unread, 1)
	require.Contains(t, doc.Unread[0], "connection refused")
	require.Equal(t, []string{"cache"}, doc.NoSource)
}

// A check that could not run at all is a fault rather than a document, and the
// fault says that the absence is not agreement.
func TestMaskCrossStore_AFailedRunIsAFaultAndNotAnEmptyPass(t *testing.T) {
	t.Parallel()
	_, fault := callMask(t,
		crossStoreReaders(nil, errors.New("CLICKHOUSE_URL is unset")),
		args("question", "cross_store"))
	require.NotNil(t, fault)
	require.Equal(t, FaultSafetyUnavailable, fault.Code)
	require.Contains(t, fault.Detail, "says nothing about whether")
}

// A server built without the reader must not answer the question, and must
// say why rather than returning a document with zeroes in it.
func TestMaskCrossStore_NoReaderIsAFault(t *testing.T) {
	t.Parallel()
	_, fault := callMask(t, maskingReaders{}, args("question", "cross_store"))
	require.NotNil(t, fault)
	require.Contains(t, fault.Detail, "not the same as the stores agreeing")
}

// The question is reachable at all, which is the whole lane in one assertion:
// the check existed, was tested, and no caller anywhere could ask for it.
func TestMaskCrossStore_TheQuestionIsInTheToolsEnum(t *testing.T) {
	t.Parallel()
	tool := maskTool(t, maskingReaders{})
	require.Contains(t, tool.Input.Properties["question"].Enum, "cross_store")
	require.Contains(t, tool.Description, "cross_store")
	require.True(t, tool.ReadOnly, "it reads catalogs and no rows")
}
