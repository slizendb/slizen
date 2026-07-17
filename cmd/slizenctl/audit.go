package main

import (
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"

	"github.com/slizendb/slizen/internal/service"
)

func auditCmd(args []string, stdout, stderr io.Writer) error {
	return auditCmdWithGet(args, stdout, stderr, httpGet)
}

func auditCmdWithGet(args []string, stdout, stderr io.Writer, get func(string) (any, error)) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	adminURL := fs.String("admin", defaultAdmin, "admin API URL")
	limit := fs.Int("limit", service.DefaultAuditLimit, "maximum audit entries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 1 || *limit > service.MaxAuditLimit {
		return errors.New("limit must be between 1 and 1000")
	}
	value, err := get(strings.TrimRight(*adminURL, "/") + "/v1/audit?limit=" + strconv.Itoa(*limit))
	return printJSON(stdout, value, err)
}
