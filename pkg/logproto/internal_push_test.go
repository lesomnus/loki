package logproto

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/grafana/loki/pkg/push"
)

func TestEncodingsAreMutuallyUndecodable(t *testing.T) {
	oneEntry := []push.Entry{entry(1, "x")}
	oneGroup := []ResourceLogs{{ScopeLogs: []ScopeLogs{{Entries: oneEntry}}}}

	tests := []struct {
		name          string
		record        interface{ Marshal() ([]byte, error) }
		decodesFlat   bool
		decodesNested bool
	}{
		{
			name:        "flat with entries and a hash",
			record:      &Stream{Labels: `{a="b"}`, Hash: 7, Entries: oneEntry},
			decodesFlat: true,
		},
		{
			name:        "flat with entries and a zero hash",
			record:      &Stream{Labels: `{a="b"}`, Entries: oneEntry},
			decodesFlat: true,
		},
		{
			name:        "flat with a hash and no entries",
			record:      &Stream{Labels: `{a="b"}`, Hash: 7},
			decodesFlat: true,
		},
		{
			name:        "flat with a single zero valued entry",
			record:      &Stream{Labels: `{a="b"}`, Entries: []push.Entry{{}}},
			decodesFlat: true,
		},
		{
			name:          "nested with groups and a hash",
			record:        &InternalStreamAdapter{Labels: `{a="b"}`, Hash: 7, ResourceLogs: oneGroup},
			decodesNested: true,
		},
		{
			name:          "nested with groups and a zero hash",
			record:        &InternalStreamAdapter{Labels: `{a="b"}`, ResourceLogs: oneGroup},
			decodesNested: true,
		},
		{
			name:          "nested with a single empty group",
			record:        &InternalStreamAdapter{Labels: `{a="b"}`, ResourceLogs: []ResourceLogs{{}}},
			decodesNested: true,
		},
		{
			// The only shape both accept, because neither carries a field the other
			// disagrees about. Asserted to decode the same either way below.
			name:          "labels alone",
			record:        &Stream{Labels: `{a="b"}`},
			decodesFlat:   true,
			decodesNested: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.record.Marshal()
			require.NoError(t, err)

			var flat Stream
			flatErr := flat.Unmarshal(data)
			var nested InternalStreamAdapter
			nestedErr := nested.Unmarshal(data)

			require.Equal(t, tt.decodesFlat, flatErr == nil, "flat decode: %v", flatErr)
			require.Equal(t, tt.decodesNested, nestedErr == nil, "nested decode: %v", nestedErr)
			require.True(t, tt.decodesFlat || tt.decodesNested, "a record must decode as one of the two")

			if tt.decodesFlat && tt.decodesNested {
				var fromNested Stream
				nested.ToStream(&fromNested)
				require.Equal(t, flat, fromNested,
					"a record both encodings accept must mean the same thing either way")
			}
		})
	}
}

func TestFromStreamRoundTripsThroughToStream(t *testing.T) {
	tests := []struct {
		name   string
		stream Stream
	}{
		{"no entries", Stream{Labels: `{a="b"}`}},
		{"no entries with a hash", Stream{Labels: `{a="b"}`, Hash: 7}},
		{"one entry", Stream{Labels: `{a="b"}`, Hash: 7, Entries: []push.Entry{entry(1, "x")}}},
		{
			name: "entries with structured metadata",
			stream: Stream{Labels: `{a="b"}`, Hash: 7, Entries: []push.Entry{
				entry(1, "x", attrs("trace_id", "1")...),
				entry(2, "y", attrs("trace_id", "2")...),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nested := FromStream(tt.stream)
			require.Equal(t, len(tt.stream.Entries), nested.entryCount())

			var got Stream
			nested.ToStream(&got)
			require.Equal(t, tt.stream, got)
		})
	}
}

func TestToStream(t *testing.T) {
	tests := []struct {
		name   string
		nested InternalStreamAdapter
		want   Stream
	}{
		{
			name:   "labels alone",
			nested: InternalStreamAdapter{Labels: `{a="b"}`},
			want:   Stream{Labels: `{a="b"}`},
		},
		{
			name:   "an empty group",
			nested: InternalStreamAdapter{Labels: `{a="b"}`, ResourceLogs: []ResourceLogs{{}}},
			want:   Stream{Labels: `{a="b"}`},
		},
		{
			name:   "an empty scope",
			nested: InternalStreamAdapter{Labels: `{a="b"}`, ResourceLogs: []ResourceLogs{{ScopeLogs: []ScopeLogs{{}}}}},
			want:   Stream{Labels: `{a="b"}`},
		},
		{
			name: "entries with nothing lifted off them",
			nested: InternalStreamAdapter{
				Labels: `{a="b"}`,
				Hash:   7,
				ResourceLogs: []ResourceLogs{
					resource(
						attrs(),
						scope(
							attrs(),
							entry(1, "x", attrs("trace_id", "1")...),
							entry(2, "y"),
						),
					),
				},
			},
			want: Stream{Labels: `{a="b"}`, Hash: 7, Entries: []push.Entry{
				entry(1, "x", attrs("trace_id", "1")...),
				entry(2, "y"),
			}},
		},
		{
			name: "resource and scope attributes resolved onto each entry",
			nested: InternalStreamAdapter{
				Labels: `{a="b"}`,
				Hash:   7,
				ResourceLogs: []ResourceLogs{
					resource(
						attrs("host", "host-1", "shared", "resource"),
						scope(
							attrs("scope", "lib", "shared", "scope"),
							entry(1, "x", attrs("shared", "entry")...),
							entry(2, "y"),
						),
					),
				},
			},
			want: Stream{Labels: `{a="b"}`, Hash: 7, Entries: []push.Entry{
				entry(1, "x", attrs("shared", "entry", "scope", "lib", "host", "host-1")...),
				entry(2, "y", attrs("scope", "lib", "shared", "scope", "host", "host-1")...),
			}},
		},
		{
			name: "entries under separate groups",
			nested: InternalStreamAdapter{
				Labels: `{a="b"}`,
				ResourceLogs: []ResourceLogs{
					resource(attrs("host", "host-1"), scope(attrs(), entry(1, "one"))),
					resource(attrs("host", "host-2"), scope(attrs(), entry(2, "two"))),
				},
			},
			want: Stream{Labels: `{a="b"}`, Entries: []push.Entry{
				entry(1, "one", attrs("host", "host-1")...),
				entry(2, "two", attrs("host", "host-2")...),
			}},
		},
		{
			name: "attributes priority",
			nested: InternalStreamAdapter{
				Labels: `{a="b"}`,
				ResourceLogs: []ResourceLogs{
					resource(
						attrs("host", "resource-1"),
						scope(
							attrs("host", "scope-11"),
							entry(111, "e111", attrs("host", "entry-111")...),
							entry(112, "e112"),
						),
						scope(
							attrs(),
							entry(121, "e121", attrs("host", "entry-121")...),
							entry(122, "e122"),
						),
					),
					resource(
						attrs(),
						scope(
							attrs("host", "scope-21"),
							entry(211, "e211", attrs("host", "entry-211")...),
							entry(212, "e212"),
						),
						scope(
							attrs(),
							entry(221, "e221", attrs("host", "entry-221")...),
							entry(222, "e222"),
						),
					),
					resource(
						attrs(),
						scope(
							attrs(),
							entry(311, "e311", attrs("host", "entry-311")...),
							entry(312, "e312"),
						),
					),
				},
			},
			want: Stream{Labels: `{a="b"}`, Entries: []push.Entry{
				// resource 1
				// -> scope 11
				entry(111, "e111", attrs("host", "entry-111")...),
				entry(112, "e112", attrs("host", "scope-11")...),
				// -> scope 12
				entry(121, "e121", attrs("host", "entry-121")...),
				entry(122, "e122", attrs("host", "resource-1")...),

				// resource 2
				// -> scope 21
				entry(211, "e211", attrs("host", "entry-211")...),
				entry(212, "e212", attrs("host", "scope-21")...),
				// -> scope 22
				entry(221, "e221", attrs("host", "entry-221")...),
				entry(222, "e222"),

				// resource 3
				entry(311, "e311", attrs("host", "entry-311")...),
				entry(312, "e312"),
			}},
		},
	}

	var got Stream
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, err := tt.nested.Marshal()
			require.NoError(t, err)

			tt.nested.ToStream(&got)
			require.Equal(t, tt.want, got)

			after, err := tt.nested.Marshal()
			require.NoError(t, err)
			require.Equal(t, before, after, "flattening a record must not modify it")
		})
	}
}

func entry(ns int64, line string, md ...push.LabelAdapter) push.Entry {
	return push.Entry{
		Timestamp:          time.Unix(0, ns),
		Line:               line,
		StructuredMetadata: md,
	}
}

func resource(attrs []push.LabelAdapter, scopes ...ScopeLogs) ResourceLogs {
	return ResourceLogs{
		Attrs:     attrs,
		ScopeLogs: scopes,
	}
}

func attrs(keyValues ...string) []push.LabelAdapter {
	if len(keyValues)%2 != 0 {
		panic("odd number of keyValues")
	}

	res := make([]push.LabelAdapter, 0, len(keyValues)/2)

	for i := 0; i < len(keyValues); i += 2 {
		res = append(res, push.LabelAdapter{Name: keyValues[i], Value: keyValues[i+1]})
	}

	return res
}

func scope(attrs []push.LabelAdapter, entries ...push.Entry) ScopeLogs {
	return ScopeLogs{
		Attrs:   attrs,
		Entries: entries,
	}
}
