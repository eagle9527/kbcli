/*
Copyright (C) 2022-2023 ApeCloud Co., Ltd

This file is part of KubeBlocks project

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package accounts

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/klog/v2"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"

	lorryutil "github.com/apecloud/kubeblocks/pkg/lorry/util"

	"github.com/apecloud/kbcli/pkg/action"
	clusterutil "github.com/apecloud/kbcli/pkg/cluster"
	"github.com/apecloud/kbcli/pkg/printer"
)

type AccountBaseOptions struct {
	ClusterName   string
	CharType      string
	ComponentName string
	PodName       string
	Pod           *corev1.Pod
	Verbose       bool
	AccountOp     lorryutil.OperationKind
	RequestMeta   map[string]interface{}
	*action.ExecOptions
}

var (
	errClusterNameNum        = fmt.Errorf("please specify ONE cluster-name at a time")
	errMissingUserName       = fmt.Errorf("please specify username")
	errMissingRoleName       = fmt.Errorf("please specify at least ONE role name")
	errInvalidRoleName       = fmt.Errorf("invalid role name, should be one of [SUPERUSER, READWRITE, READONLY] ")
	errCompNameOrInstName    = fmt.Errorf("please specify either --component or --instance, they are exclusive")
	errClusterNameorInstName = fmt.Errorf("specify either cluster name or --instance")
)

func NewAccountBaseOptions(f cmdutil.Factory, streams genericiooptions.IOStreams) *AccountBaseOptions {
	return &AccountBaseOptions{
		ExecOptions: action.NewExecOptions(f, streams),
	}
}

func (o *AccountBaseOptions) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.ComponentName, "component", "", "Specify the name of component to be connected. If not specified, pick the first one.")
	cmd.Flags().StringVarP(&o.PodName, "instance", "i", "", "Specify the name of instance to be connected.")
}

func (o *AccountBaseOptions) Validate(args []string) error {
	if len(args) > 1 {
		return errClusterNameNum
	}

	if len(o.PodName) > 0 {
		if len(o.ComponentName) > 0 {
			return errCompNameOrInstName
		}
		if len(args) > 0 {
			return errClusterNameorInstName
		}
	} else if len(args) == 0 {
		return errClusterNameorInstName
	}
	if len(args) == 1 {
		o.ClusterName = args[0]
	}
	return nil
}

func (o *AccountBaseOptions) Complete(f cmdutil.Factory) error {
	var err error
	err = o.ExecOptions.Complete()
	if err != nil {
		return err
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()

	if len(o.PodName) > 0 {
		// get pod by name
		o.Pod, err = o.ExecOptions.Client.CoreV1().Pods(o.Namespace).Get(ctx, o.PodName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		o.ClusterName = clusterutil.GetPodClusterName(o.Pod)
		o.ComponentName = clusterutil.GetPodComponentName(o.Pod)
	}

	compInfo, err := clusterutil.FillCompInfoByName(ctx, o.ExecOptions.Dynamic, o.Namespace, o.ClusterName, o.ComponentName)
	if err != nil {
		return err
	}
	// fill component name
	if len(o.ComponentName) == 0 {
		o.ComponentName = compInfo.Component.Name
	}
	// fill character type
	o.CharType = compInfo.ComponentDef.CharacterType

	if len(o.PodName) == 0 {
		if o.PodName, err = compInfo.InferPodName(); err != nil {
			return err
		}
		// get pod by name
		o.Pod, err = o.ExecOptions.Client.CoreV1().Pods(o.Namespace).Get(ctx, o.PodName, metav1.GetOptions{})
		if err != nil {
			return err
		}
	}

	o.ExecOptions.Pod = o.Pod
	o.ExecOptions.Namespace = o.Namespace
	o.ExecOptions.Quiet = true
	o.ExecOptions.TTY = true
	o.ExecOptions.Stdin = true

	o.Verbose = klog.V(1).Enabled()

	return nil
}

func (o *AccountBaseOptions) newTblPrinterWithStyle(title string, header []interface{}) *printer.TablePrinter {
	tblPrinter := printer.NewTablePrinter(o.Out)
	tblPrinter.SetStyle(printer.TerminalStyle)
	// tblPrinter.Tbl.SetTitle(title)
	tblPrinter.SetHeader(header...)
	return tblPrinter
}

func (o *AccountBaseOptions) printGeneralInfo(event, message string) {
	tblPrinter := o.newTblPrinterWithStyle("QUERY RESULT", []interface{}{"RESULT", "MESSAGE"})
	tblPrinter.AddRow(event, message)
	tblPrinter.Print()
}

func (o *AccountBaseOptions) printUserInfo(users []map[string]any) {
	// render user info with username and password expired boolean
	tblPrinter := o.newTblPrinterWithStyle("USER INFO", []interface{}{"USERNAME", "EXPIRED"})
	for _, user := range users {
		tblPrinter.AddRow(user["userName"], user["expired"])
	}

	tblPrinter.Print()
}

func (o *AccountBaseOptions) printRoleInfo(users []map[string]any) {
	tblPrinter := o.newTblPrinterWithStyle("USER INFO", []interface{}{"USERNAME", "ROLE"})
	for _, user := range users {
		tblPrinter.AddRow(user["userName"], user["roleName"])
	}
	tblPrinter.Print()
}
