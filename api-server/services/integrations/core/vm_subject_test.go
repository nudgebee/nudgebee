package core

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestInstanceHost(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{name: "ipv4 with port", labels: map[string]string{"instance": "10.0.0.5:9100"}, want: "10.0.0.5"},
		{name: "ipv6 with port", labels: map[string]string{"instance": "[fe80::1]:9100"}, want: "fe80::1"},
		{name: "no port", labels: map[string]string{"instance": "10.0.0.5"}, want: "10.0.0.5"},
		{name: "absent", labels: map[string]string{}, want: ""},
		{name: "nil", labels: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, instanceHost(tt.labels))
		})
	}
}

func TestResolveVMSubject(t *testing.T) {
	const query = "FROM cloud_resourses cr"
	vmRows := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "name"}) }

	t.Run("instance id and host are passed through", func(t *testing.T) {
		pkgMock.ExpectQuery(query).
			WithArgs(testTenant, "acct-1", "i-0abc", "vm-10.0.0.5", "10.0.0.5").
			WillReturnRows(vmRows().AddRow("res-1", "web-1"))
		got, ok := ResolveVMSubject(ctxForTenant(t, testTenant), "acct-1", VMSubjectInput{
			Labels: map[string]string{"instance": "10.0.0.5:9100", "instance_id": "i-0abc"},
		})
		assert.True(t, ok)
		assert.Equal(t, VMSubject{CloudResourceID: "res-1", Name: "web-1"}, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("host only", func(t *testing.T) {
		pkgMock.ExpectQuery(query).
			WithArgs(testTenant, "acct-1", "", "vm-10.0.0.5", "10.0.0.5").
			WillReturnRows(vmRows().AddRow("res-1", "web-1"))
		got, ok := ResolveVMSubject(ctxForTenant(t, testTenant), "acct-1", VMSubjectInput{
			Labels: map[string]string{"instance": "10.0.0.5:9100"},
		})
		assert.True(t, ok)
		assert.Equal(t, "res-1", got.CloudResourceID)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("miss is not resolved", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WillReturnRows(vmRows())
		_, ok := ResolveVMSubject(ctxForTenant(t, testTenant), "acct-1", VMSubjectInput{
			Labels: map[string]string{"instance": "10.0.0.5:9100"},
		})
		assert.False(t, ok)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("query error is not resolved", func(t *testing.T) {
		pkgMock.ExpectQuery(query).WillReturnError(errors.New("boom"))
		_, ok := ResolveVMSubject(ctxForTenant(t, testTenant), "acct-1", VMSubjectInput{
			Labels: map[string]string{"instance": "10.0.0.5:9100"},
		})
		assert.False(t, ok)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("no identifier or account skips the query", func(t *testing.T) {
		sc := ctxForTenant(t, testTenant)
		_, ok := ResolveVMSubject(sc, "acct-1", VMSubjectInput{Labels: map[string]string{"alertname": "HighCPU"}})
		assert.False(t, ok)
		_, ok = ResolveVMSubject(sc, "", VMSubjectInput{Labels: map[string]string{"instance": "10.0.0.5:9100"}})
		assert.False(t, ok)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}
