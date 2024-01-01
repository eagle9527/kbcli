package main

import (
	"github.com/apecloud/kbcli/pkg/cmd/cluster"
	"github.com/apecloud/kbcli/pkg/util"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"os"
)

func main() {
	kubeconfig := "/Users/eagle/.kube/config-kind1"
	kubeConfigFlags := util.NewConfigFlagNoWarnings()
	kubeConfigFlags.KubeConfig = &kubeconfig
	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(kubeConfigFlags)
	f := cmdutil.NewFactory(matchVersionKubeConfigFlags)
	var streams genericiooptions.IOStreams
	o := cluster.NewCreateOptions(f, streams)
	o.Args = []string{"postgresql-1"}
	o.Values = []string{"cpu=1", "memory=1Gi", "storage=20Gi", "replicas=1"}
	o.ClusterDefRef = "postgresql"
	o.ClusterVersionRef = "postgresql-12.14.0"
	o.Out = os.Stdout
	o.TerminationPolicy = "Delete"
	cmdutil.CheckErr(o.CreateOptions.Complete())
	cmdutil.CheckErr(o.Complete())
	cmdutil.CheckErr(o.Validate())
	cmdutil.CheckErr(o.Run())
}
