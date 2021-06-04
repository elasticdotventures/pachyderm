package transform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/pachyderm/pachyderm/v2/src/client"
	col "github.com/pachyderm/pachyderm/v2/src/internal/collection"
	"github.com/pachyderm/pachyderm/v2/src/internal/require"
	"github.com/pachyderm/pachyderm/v2/src/internal/serviceenv"
	"github.com/pachyderm/pachyderm/v2/src/internal/testpachd"
	"github.com/pachyderm/pachyderm/v2/src/internal/work"
	"github.com/pachyderm/pachyderm/v2/src/pfs"
	"github.com/pachyderm/pachyderm/v2/src/pps"
	"github.com/pachyderm/pachyderm/v2/src/server/worker/common"
	"github.com/pachyderm/pachyderm/v2/src/server/worker/driver"
	"github.com/pachyderm/pachyderm/v2/src/server/worker/logs"
)

// Set this to true to enable worker log statements to go to stdout
const debug = false

func defaultPipelineInfo() *pps.PipelineInfo {
	name := "testPipeline"
	return &pps.PipelineInfo{
		Pipeline:     client.NewPipeline(name),
		OutputBranch: "master",
		Transform: &pps.Transform{
			Cmd:        []string{"bash"},
			Stdin:      []string{"cp inputRepo/* out"},
			WorkingDir: client.PPSInputPrefix,
		},
		ParallelismSpec: &pps.ParallelismSpec{
			Constant: 1,
		},
		ResourceRequests: &pps.ResourceSpec{
			Memory: "100M",
			Cpu:    0.5,
		},
		Input: &pps.Input{
			Pfs: &pps.PFSInput{
				Name:   "inputRepo",
				Repo:   "inputRepo",
				Branch: "master",
				Glob:   "/*",
			},
		},
		SpecCommit: client.NewSystemRepo(name, pfs.SpecRepoType).NewCommit("master", ""),
	}
}

type testEnv struct {
	*testpachd.RealEnv
	logger *logs.MockLogger
	driver driver.Driver
}

// testDriver is identical to a real driver except it overloads egress, which is
// tricky to do in a test environment
type testDriver struct {
	inner driver.Driver
}

func (td *testDriver) Jobs() col.PostgresCollection {
	return td.inner.Jobs()
}
func (td *testDriver) Pipelines() col.PostgresCollection {
	return td.inner.Pipelines()
}
func (td *testDriver) NewTaskWorker() *work.Worker {
	return td.inner.NewTaskWorker()
}
func (td *testDriver) NewTaskQueue() (*work.TaskQueue, error) {
	return td.inner.NewTaskQueue()
}
func (td *testDriver) PipelineInfo() *pps.PipelineInfo {
	return td.inner.PipelineInfo()
}
func (td *testDriver) Namespace() string {
	return td.inner.Namespace()
}
func (td *testDriver) InputDir() string {
	return td.inner.InputDir()
}
func (td *testDriver) PachClient() *client.APIClient {
	return td.inner.PachClient()
}
func (td *testDriver) ExpectedNumWorkers() (int64, error) {
	return td.inner.ExpectedNumWorkers()
}
func (td *testDriver) WithContext(ctx context.Context) driver.Driver {
	return &testDriver{td.inner.WithContext(ctx)}
}
func (td *testDriver) WithActiveData(inputs []*common.Input, dir string, cb func() error) error {
	return td.inner.WithActiveData(inputs, dir, cb)
}
func (td *testDriver) UserCodeEnv(jobID string, commit *pfs.Commit, inputs []*common.Input) []string {
	return td.inner.UserCodeEnv(jobID, commit, inputs)
}
func (td *testDriver) RunUserCode(ctx context.Context, logger logs.TaggedLogger, env []string) error {
	return td.inner.RunUserCode(ctx, logger, env)
}
func (td *testDriver) RunUserErrorHandlingCode(ctx context.Context, logger logs.TaggedLogger, env []string) error {
	return td.inner.RunUserErrorHandlingCode(ctx, logger, env)
}
func (td *testDriver) DeleteJob(sqlTx *sqlx.Tx, ji *pps.StoredJobInfo) error {
	return td.inner.DeleteJob(sqlTx, ji)
}
func (td *testDriver) UpdateJobState(jobID string, state pps.JobState, reason string) error {
	return td.inner.UpdateJobState(jobID, state, reason)
}
func (td *testDriver) NewSQLTx(cb func(*sqlx.Tx) error) error {
	return td.inner.NewSQLTx(cb)
}

// newTestEnv provides a test env with etcd and pachd instances and connected
// clients, plus a worker driver for performing worker operations.
func newTestEnv(t *testing.T, dbConfig serviceenv.ConfigOption, pipelineInfo *pps.PipelineInfo) *testEnv {
	realEnv := testpachd.NewRealEnv(t, dbConfig)
	logger := logs.NewMockLogger()
	if debug {
		logger.Writer = os.Stdout
	}
	workerDir := filepath.Join(realEnv.Directory, "worker")
	driver, err := driver.NewDriver(
		realEnv.ServiceEnv,
		realEnv.PachClient,
		pipelineInfo,
		workerDir,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(realEnv.PachClient.Ctx())
	t.Cleanup(cancel)
	driver = driver.WithContext(ctx)

	return &testEnv{
		RealEnv: realEnv,
		logger:  logger,
		driver:  &testDriver{driver},
	}
}
