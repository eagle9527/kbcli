package main

import (
	"os"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"

	"github.com/apecloud/kbcli/pkg/action"
	"github.com/apecloud/kbcli/pkg/cmd/cluster"
	"github.com/apecloud/kbcli/pkg/types"
	"github.com/apecloud/kbcli/pkg/util"
)

func NewCreateOptions(f cmdutil.Factory, streams genericiooptions.IOStreams) *cluster.CreateOptions {
	o := &cluster.CreateOptions{CreateOptions: action.CreateOptions{
		Factory:         f,
		IOStreams:       streams,
		CueTemplateName: cluster.CueTemplateName,
		GVR:             types.ClusterGVR(),
	}}
	o.CreateOptions.Options = o
	o.CreateOptions.PreCreate = o.PreCreate
	o.CreateOptions.CreateDependencies = o.CreateDependencies
	o.CreateOptions.CleanUpFn = o.CleanUp
	return o
}

func main() {
	kubeConfigFlags := util.NewConfigFlagNoWarnings()
	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(kubeConfigFlags)
	f := cmdutil.NewFactory(matchVersionKubeConfigFlags)
	var streams genericiooptions.IOStreams
	o := NewCreateOptions(f, streams)
	o.Args = []string{"mysql-cluster1"}
	o.Values = []string{"cpu=1", "memory=1Gi", "storage=20Gi", "replicas=1"}
	o.ClusterDefRef = "apecloud-mysql"
	o.ClusterVersionRef = "ac-mysql-8.0.30"
	o.Out = os.Stdout
	o.TerminationPolicy = "Delete"
	cmdutil.CheckErr(o.CreateOptions.Complete())
	cmdutil.CheckErr(o.Complete())
	cmdutil.CheckErr(o.Validate())
	cmdutil.CheckErr(o.Run())
}
