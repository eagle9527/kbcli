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

package cluster

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/exp/maps"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/jsonpath"
	"k8s.io/kubectl/pkg/cmd/get"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/cmd/util/editor"
	"k8s.io/kubectl/pkg/util/templates"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1alpha1 "github.com/apecloud/kubeblocks/apis/apps/v1alpha1"
	dpv1alpha1 "github.com/apecloud/kubeblocks/apis/dataprotection/v1alpha1"
	"github.com/apecloud/kubeblocks/pkg/constant"
	dptypes "github.com/apecloud/kubeblocks/pkg/dataprotection/types"

	"github.com/apecloud/kbcli/pkg/action"
	"github.com/apecloud/kbcli/pkg/cluster"
	"github.com/apecloud/kbcli/pkg/printer"
	"github.com/apecloud/kbcli/pkg/types"
	"github.com/apecloud/kbcli/pkg/util"
)

var (
	listBackupPolicyExample = templates.Examples(`
		# list all backup policies
		kbcli cluster list-backup-policy

		# using short cmd to list backup policy of the specified cluster
        kbcli cluster list-bp mycluster
	`)
	editExample = templates.Examples(`
		# edit backup policy
		kbcli cluster edit-backup-policy <backup-policy-name>

        # update backup Repo
		kbcli cluster edit-backup-policy <backup-policy-name> --set backupRepoName=<backup-repo-name>

	    # using short cmd to edit backup policy
        kbcli cluster edit-bp <backup-policy-name>
	`)
	createBackupExample = templates.Examples(`
		# Create a backup for the cluster, use the default backup policy and volume snapshot backup method
		kbcli cluster backup mycluster

		# create a backup with a specified method, run "kbcli cluster desc-backup-policy mycluster" to show supported backup methods
		kbcli cluster backup mycluster --method volume-snapshot

		# create a backup with specified backup policy, run "kbcli cluster list-backup-policy mycluster" to show the cluster supported backup policies
		kbcli cluster backup mycluster --method volume-snapshot --policy <backup-policy-name>

		# create a backup from a parent backup
		kbcli cluster backup mycluster --parent-backup parent-backup-name
	`)
	listBackupExample = templates.Examples(`
		# list all backups
		kbcli cluster list-backups
	`)
	deleteBackupExample = templates.Examples(`
		# delete a backup named backup-name
		kbcli cluster delete-backup cluster-name --name backup-name
	`)
	createRestoreExample = templates.Examples(`
		# restore a new cluster from a backup
		kbcli cluster restore new-cluster-name --backup backup-name
	`)
	describeBackupExample = templates.Examples(`
		# describe a backup
		kbcli cluster describe-backup backup-default-mycluster-20230616190023
	`)
	describeBackupPolicyExample = templates.Examples(`
		# describe the default backup policy of the cluster
		kbcli cluster describe-backup-policy cluster-name

		# describe the backup policy of the cluster with specified name
		kbcli cluster describe-backup-policy cluster-name --name backup-policy-name
	`)
)

const TrueValue = "true"

type CreateBackupOptions struct {
	BackupSpec     appsv1alpha1.BackupSpec `json:"backupSpec"`
	ClusterRef     string                  `json:"clusterRef"`
	OpsType        string                  `json:"opsType"`
	OpsRequestName string                  `json:"opsRequestName"`

	action.CreateOptions `json:"-"`
}

type ListBackupOptions struct {
	*action.ListOptions
	BackupName string
}

type DescribeBackupOptions struct {
	Factory   cmdutil.Factory
	client    clientset.Interface
	dynamic   dynamic.Interface
	namespace string

	// resource type and names
	Gvr   schema.GroupVersionResource
	names []string

	genericiooptions.IOStreams
}

func (o *CreateBackupOptions) CompleteBackup() error {
	if err := o.Complete(); err != nil {
		return err
	}
	// generate backupName
	if len(o.BackupSpec.BackupName) == 0 {
		o.BackupSpec.BackupName = strings.Join([]string{"backup", o.Namespace, o.Name, time.Now().Format("20060102150405")}, "-")
	}

	// set ops type, ops request name and clusterRef
	o.OpsType = string(appsv1alpha1.BackupType)
	o.OpsRequestName = o.BackupSpec.BackupName
	o.ClusterRef = o.Name

	return o.CreateOptions.Complete()
}

func (o *CreateBackupOptions) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("missing cluster name")
	}

	// if backup policy is not specified, use the default backup policy
	if o.BackupSpec.BackupPolicyName == "" {
		if err := o.completeDefaultBackupPolicy(); err != nil {
			return err
		}
	}

	// check if backup policy exists
	backupPolicyObj, err := o.Dynamic.Resource(types.BackupPolicyGVR()).Namespace(o.Namespace).Get(context.TODO(), o.BackupSpec.BackupPolicyName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	backupPolicy := &dpv1alpha1.BackupPolicy{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(backupPolicyObj.Object, backupPolicy); err != nil {
		return err
	}

	if o.BackupSpec.BackupMethod == "" {
		return fmt.Errorf("backup method can not be empty, you can specify it by --method")
	}
	// TODO: check if pvc exists

	// valid retention period
	if o.BackupSpec.RetentionPeriod != "" {
		_, err := dpv1alpha1.RetentionPeriod(o.BackupSpec.RetentionPeriod).ToDuration()
		if err != nil {
			return fmt.Errorf("invalid retention period, please refer to examples [1y, 1m, 1d, 1h, 1m] or combine them [1y1m1d1h1m]")
		}
	}

	// check if parent backup exists
	if o.BackupSpec.ParentBackupName != "" {
		parentBackupObj, err := o.Dynamic.Resource(types.BackupGVR()).Namespace(o.Namespace).Get(context.TODO(), o.BackupSpec.ParentBackupName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		parentBackup := &dpv1alpha1.Backup{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(parentBackupObj.Object, parentBackup); err != nil {
			return err
		}
		if parentBackup.Status.Phase != dpv1alpha1.BackupPhaseCompleted {
			return fmt.Errorf("parent backup %s is not completed", o.BackupSpec.ParentBackupName)
		}
		if parentBackup.Labels[constant.AppInstanceLabelKey] != o.Name {
			return fmt.Errorf("parent backup %s is not belong to cluster %s", o.BackupSpec.ParentBackupName, o.Name)
		}
	}
	return nil
}

// completeDefaultBackupPolicy completes the default backup policy.
func (o *CreateBackupOptions) completeDefaultBackupPolicy() error {
	defaultBackupPolicyName, err := o.getDefaultBackupPolicy()
	if err != nil {
		return err
	}
	o.BackupSpec.BackupPolicyName = defaultBackupPolicyName
	return nil
}

func (o *CreateBackupOptions) getDefaultBackupPolicy() (string, error) {
	clusterObj, err := o.Dynamic.Resource(types.ClusterGVR()).Namespace(o.Namespace).Get(context.TODO(), o.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	// TODO: support multiple components backup, add --componentDef flag
	opts := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s",
			constant.AppInstanceLabelKey, clusterObj.GetName()),
	}
	objs, err := o.Dynamic.
		Resource(types.BackupPolicyGVR()).Namespace(o.Namespace).
		List(context.TODO(), opts)
	if err != nil {
		return "", err
	}
	if len(objs.Items) == 0 {
		return "", fmt.Errorf(`not found any backup policy for cluster "%s"`, o.Name)
	}
	var defaultBackupPolicies []unstructured.Unstructured
	for _, obj := range objs.Items {
		if obj.GetAnnotations()[dptypes.DefaultBackupPolicyAnnotationKey] == TrueValue {
			defaultBackupPolicies = append(defaultBackupPolicies, obj)
		}
	}
	if len(defaultBackupPolicies) == 0 {
		return "", fmt.Errorf(`not found any default backup policy for cluster "%s"`, o.Name)
	}
	if len(defaultBackupPolicies) > 1 {
		return "", fmt.Errorf(`cluster "%s" has multiple default backup policies`, o.Name)
	}
	return defaultBackupPolicies[0].GetName(), nil
}

func NewCreateBackupCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	customOutPut := func(opt *action.CreateOptions) {
		output := fmt.Sprintf("Backup %s created successfully, you can view the progress:", opt.Name)
		printer.PrintLine(output)
		nextLine := fmt.Sprintf("\tkbcli cluster list-backups --name=%s -n %s", opt.Name, opt.Namespace)
		printer.PrintLine(nextLine)
	}

	o := &CreateBackupOptions{
		CreateOptions: action.CreateOptions{
			IOStreams:       streams,
			Factory:         f,
			GVR:             types.OpsGVR(),
			CueTemplateName: "opsrequest_template.cue",
			CustomOutPut:    customOutPut,
		},
	}
	o.CreateOptions.Options = o

	cmd := &cobra.Command{
		Use:               "backup NAME",
		Short:             "Create a backup for the cluster.",
		Example:           createBackupExample,
		ValidArgsFunction: util.ResourceNameCompletionFunc(f, types.ClusterGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			o.Args = args
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			cmdutil.CheckErr(o.CompleteBackup())
			cmdutil.CheckErr(o.Validate())
			cmdutil.CheckErr(o.Run())
		},
	}

	cmd.Flags().StringVar(&o.BackupSpec.BackupMethod, "method", "", "Backup methods are defined in backup policy (required), if only one backup method in backup policy, use it as default backup method, if multiple backup methods in backup policy, use method which volume snapshot is true as default backup method")
	cmd.Flags().StringVar(&o.BackupSpec.BackupName, "name", "", "Backup name")
	cmd.Flags().StringVar(&o.BackupSpec.BackupPolicyName, "policy", "", "Backup policy name, if not specified, use the cluster default backup policy")
	cmd.Flags().StringVar(&o.BackupSpec.DeletionPolicy, "deletion-policy", "Delete", "Deletion policy for backup, determine whether the backup content in backup repo will be deleted after the backup is deleted, supported values: [Delete, Retain]")
	cmd.Flags().StringVar(&o.BackupSpec.RetentionPeriod, "retention-period", "", "Retention period for backup, supported values: [1y, 1mo, 1d, 1h, 1m] or combine them [1y1mo1d1h1m], if not specified, the backup will not be automatically deleted, you need to manually delete it.")
	cmd.Flags().StringVar(&o.BackupSpec.ParentBackupName, "parent-backup", "", "Parent backup name, used for incremental backup")
	// register backup flag completion func
	o.RegisterBackupFlagCompletionFunc(cmd, f)
	return cmd
}
func (o *CreateBackupOptions) RegisterBackupFlagCompletionFunc(cmd *cobra.Command, f cmdutil.Factory) {
	getClusterName := func(cmd *cobra.Command, args []string) string {
		clusterName, _ := cmd.Flags().GetString("cluster")
		if clusterName != "" {
			return clusterName
		}
		if len(args) > 0 {
			return args[0]
		}
		return ""
	}
	util.CheckErr(cmd.RegisterFlagCompletionFunc(
		"deletion-policy",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return []string{string(dpv1alpha1.BackupDeletionPolicyRetain), string(dpv1alpha1.BackupDeletionPolicyDelete)}, cobra.ShellCompDirectiveNoFileComp
		}))

	util.CheckErr(cmd.RegisterFlagCompletionFunc(
		"policy",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			label := fmt.Sprintf("%s=%s", constant.AppInstanceLabelKey, getClusterName(cmd, args))
			return util.CompGetResourceWithLabels(f, cmd, util.GVRToString(types.BackupPolicyGVR()), []string{label}, toComplete), cobra.ShellCompDirectiveNoFileComp
		}))

	util.CheckErr(cmd.RegisterFlagCompletionFunc(
		"method",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			namespace, _ := cmd.Flags().GetString("namespace")
			if namespace == "" {
				namespace, _, _ = f.ToRawKubeConfigLoader().Namespace()
			}
			var (
				labelSelector string
				clusterName   = getClusterName(cmd, args)
			)
			if clusterName != "" {
				labelSelector = fmt.Sprintf("%s=%s", constant.AppInstanceLabelKey, clusterName)
			}
			dynamicClient, _ := f.DynamicClient()
			objs, _ := dynamicClient.Resource(types.BackupPolicyGVR()).Namespace(namespace).List(context.TODO(), metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			methodMap := map[string]struct{}{}
			for _, v := range objs.Items {
				backupPolicy := &dpv1alpha1.BackupPolicy{}
				_ = runtime.DefaultUnstructuredConverter.FromUnstructured(v.Object, backupPolicy)
				for _, m := range backupPolicy.Spec.BackupMethods {
					methodMap[m.Name] = struct{}{}
				}
			}
			return maps.Keys(methodMap), cobra.ShellCompDirectiveNoFileComp
		}))
}

func PrintBackupList(o ListBackupOptions) error {
	var backupNameMap = make(map[string]bool)
	for _, name := range o.Names {
		backupNameMap[name] = true
	}

	// if format is JSON or YAML, use default printer to output the result.
	if o.Format == printer.JSON || o.Format == printer.YAML {
		if o.BackupName != "" {
			o.Names = []string{o.BackupName}
		}
		_, err := o.Run()
		return err
	}
	dynamic, err := o.Factory.DynamicClient()
	if err != nil {
		return err
	}
	if o.AllNamespaces {
		o.Namespace = ""
	}
	backupList, err := dynamic.Resource(types.BackupGVR()).Namespace(o.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: o.LabelSelector,
		FieldSelector: o.FieldSelector,
	})
	if err != nil {
		return err
	}

	if len(backupList.Items) == 0 {
		o.PrintNotFoundResources()
		return nil
	}

	// sort the unstructured objects with the creationTimestamp in positive order
	sort.Sort(unstructuredList(backupList.Items))
	tbl := printer.NewTablePrinter(o.Out)
	tbl.SetHeader("NAME", "NAMESPACE", "SOURCE-CLUSTER", "METHOD", "STATUS", "TOTAL-SIZE", "DURATION", "CREATE-TIME", "COMPLETION-TIME", "EXPIRATION")
	for _, obj := range backupList.Items {
		backup := &dpv1alpha1.Backup{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, backup); err != nil {
			return err
		}
		// TODO(ldm): find cluster from backup policy target spec.
		sourceCluster := backup.Labels[constant.AppInstanceLabelKey]
		durationStr := ""
		if backup.Status.Duration != nil {
			durationStr = duration.HumanDuration(backup.Status.Duration.Duration)
		}
		statusString := string(backup.Status.Phase)
		if len(o.Names) > 0 && !backupNameMap[backup.Name] {
			continue
		}
		var availableReplicas *int32
		for _, v := range backup.Status.Actions {
			if v.ActionType == dpv1alpha1.ActionTypeStatefulSet {
				availableReplicas = v.AvailableReplicas
				break
			}
		}
		if availableReplicas != nil {
			statusString = fmt.Sprintf("%s(AvailablePods: %d)", statusString, availableReplicas)
		}
		tbl.AddRow(backup.Name, backup.Namespace, sourceCluster, backup.Spec.BackupMethod, statusString, backup.Status.TotalSize,
			durationStr, util.TimeFormat(&backup.CreationTimestamp), util.TimeFormat(backup.Status.CompletionTimestamp),
			util.TimeFormat(backup.Status.Expiration))
	}
	tbl.Print()
	return nil
}

func NewListBackupCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := &ListBackupOptions{ListOptions: action.NewListOptions(f, streams, types.BackupGVR())}
	cmd := &cobra.Command{
		Use:               "list-backups",
		Short:             "List backups.",
		Aliases:           []string{"ls-backups"},
		Example:           listBackupExample,
		ValidArgsFunction: util.ResourceNameCompletionFunc(f, types.ClusterGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			o.LabelSelector = util.BuildLabelSelectorByNames(o.LabelSelector, args)
			if o.BackupName != "" {
				o.Names = []string{o.BackupName}
			}
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			util.CheckErr(o.Complete())
			util.CheckErr(PrintBackupList(*o))
		},
	}
	o.AddFlags(cmd)
	cmd.Flags().StringVar(&o.BackupName, "name", "", "The backup name to get the details.")
	return cmd
}

func NewDescribeBackupCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := &DescribeBackupOptions{
		Factory:   f,
		IOStreams: streams,
		Gvr:       types.BackupGVR(),
	}
	cmd := &cobra.Command{
		Use:               "describe-backup BACKUP-NAME",
		Short:             "Describe a backup.",
		Aliases:           []string{"desc-backup"},
		Example:           describeBackupExample,
		ValidArgsFunction: util.ResourceNameCompletionFunc(f, types.BackupGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			util.CheckErr(o.Complete(args))
			util.CheckErr(o.Run())
		},
	}
	return cmd
}

func NewDeleteBackupCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := action.NewDeleteOptions(f, streams, types.BackupGVR())
	cmd := &cobra.Command{
		Use:               "delete-backup",
		Short:             "Delete a backup.",
		Example:           deleteBackupExample,
		ValidArgsFunction: util.ResourceNameCompletionFunc(f, types.ClusterGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			util.CheckErr(completeForDeleteBackup(o, args))
			util.CheckErr(o.Run())
		},
	}
	cmd.Flags().StringSliceVar(&o.Names, "name", []string{}, "Backup names")
	o.AddFlags(cmd)
	return cmd
}

// completeForDeleteBackup completes cmd for delete backup
func completeForDeleteBackup(o *action.DeleteOptions, args []string) error {
	if len(args) == 0 {
		return errors.New("Missing cluster name")
	}
	if len(args) > 1 {
		return errors.New("Only supported delete the Backup of one cluster")
	}
	if !o.Force && len(o.Names) == 0 {
		return errors.New("Missing --name as backup name.")
	}
	if o.Force && len(o.Names) == 0 {
		// do force action, for --force and --name unset, delete all backups of the cluster
		// if backup name unset and cluster name set, delete all backups of the cluster
		o.LabelSelector = util.BuildLabelSelectorByNames(o.LabelSelector, args)
		o.ConfirmedNames = args
	}
	o.ConfirmedNames = o.Names
	return nil
}

type CreateRestoreOptions struct {
	RestoreSpec    appsv1alpha1.RestoreSpec `json:"restoreSpec"`
	ClusterRef     string                   `json:"clusterRef"`
	OpsType        string                   `json:"opsType"`
	OpsRequestName string                   `json:"opsRequestName"`

	action.CreateOptions `json:"-"`
}

func (o *CreateRestoreOptions) Validate() error {
	if o.RestoreSpec.BackupName == "" {
		return fmt.Errorf("must be specified one of the --backup ")
	}

	if o.Name == "" {
		name, err := generateClusterName(o.Dynamic, o.Namespace)
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("failed to generate a random cluster name")
		}
		o.Name = name
	}

	// set ops type, ops request name and clusterRef
	o.OpsType = string(appsv1alpha1.RestoreType)
	o.ClusterRef = o.Name
	o.OpsRequestName = o.Name

	return nil
}

func NewCreateRestoreCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	customOutPut := func(opt *action.CreateOptions) {
		output := fmt.Sprintf("Cluster %s created", opt.Name)
		printer.PrintLine(output)
	}

	o := &CreateRestoreOptions{}
	o.CreateOptions = action.CreateOptions{
		IOStreams:       streams,
		Factory:         f,
		Options:         o,
		GVR:             types.OpsGVR(),
		CueTemplateName: "opsrequest_template.cue",
		CustomOutPut:    customOutPut,
	}

	cmd := &cobra.Command{
		Use:     "restore",
		Short:   "Restore a new cluster from backup.",
		Example: createRestoreExample,
		Run: func(cmd *cobra.Command, args []string) {
			o.Args = args
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			util.CheckErr(o.Complete())
			util.CheckErr(o.Validate())
			util.CheckErr(o.Run())
		},
	}
	cmd.Flags().StringVar(&o.RestoreSpec.BackupName, "backup", "", "Backup name")
	cmd.Flags().StringVar(&o.RestoreSpec.RestoreTimeStr, "restore-to-time", "", "point in time recovery(PITR)")
	cmd.Flags().StringVar(&o.RestoreSpec.VolumeRestorePolicy, "volume-restore-policy", "Parallel", "the volume claim restore policy, supported values: [Serial, Parallel]")
	return cmd
}

func NewListBackupPolicyCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := action.NewListOptions(f, streams, types.BackupPolicyGVR())
	cmd := &cobra.Command{
		Use:               "list-backup-policy",
		Short:             "List backups policies.",
		Aliases:           []string{"list-bp"},
		Example:           listBackupPolicyExample,
		ValidArgsFunction: util.ResourceNameCompletionFunc(f, types.ClusterGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			o.LabelSelector = util.BuildLabelSelectorByNames(o.LabelSelector, args)
			o.Names = nil
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			util.CheckErr(o.Complete())
			util.CheckErr(PrintBackupPolicyList(*o))
		},
	}
	o.AddFlags(cmd)
	return cmd
}

// PrintBackupPolicyList prints the backup policy list.
func PrintBackupPolicyList(o action.ListOptions) error {
	var backupPolicyNameMap = make(map[string]bool)
	for _, name := range o.Names {
		backupPolicyNameMap[name] = true
	}

	// if format is JSON or YAML, use default printer to output the result.
	if o.Format == printer.JSON || o.Format == printer.YAML {
		_, err := o.Run()
		return err
	}
	dynamic, err := o.Factory.DynamicClient()
	if err != nil {
		return err
	}
	if o.AllNamespaces {
		o.Namespace = ""
	}
	backupPolicyList, err := dynamic.Resource(types.BackupPolicyGVR()).Namespace(o.Namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: o.LabelSelector,
		FieldSelector: o.FieldSelector,
	})
	if err != nil {
		return err
	}

	if len(backupPolicyList.Items) == 0 {
		o.PrintNotFoundResources()
		return nil
	}

	tbl := printer.NewTablePrinter(o.Out)
	tbl.SetHeader("NAME", "NAMESPACE", "DEFAULT", "CLUSTER", "CREATE-TIME", "STATUS")
	for _, obj := range backupPolicyList.Items {
		defaultPolicy, ok := obj.GetAnnotations()[dptypes.DefaultBackupPolicyAnnotationKey]
		backupPolicy := &dpv1alpha1.BackupPolicy{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, backupPolicy); err != nil {
			return err
		}
		if !ok {
			defaultPolicy = "false"
		}
		if len(o.Names) > 0 && !backupPolicyNameMap[backupPolicy.Name] {
			continue
		}
		createTime := obj.GetCreationTimestamp()
		tbl.AddRow(obj.GetName(), obj.GetNamespace(), defaultPolicy, obj.GetLabels()[constant.AppInstanceLabelKey],
			util.TimeFormat(&createTime), backupPolicy.Status.Phase)
	}
	tbl.Print()
	return nil
}

type updateBackupPolicyFieldFunc func(backupPolicy *dpv1alpha1.BackupPolicy, targetVal string) error

type editBackupPolicyOptions struct {
	namespace string
	name      string
	dynamic   dynamic.Interface
	Factory   cmdutil.Factory

	GVR schema.GroupVersionResource
	genericiooptions.IOStreams
	editContent       []editorRow
	editContentKeyMap map[string]updateBackupPolicyFieldFunc
	original          string
	target            string
	values            []string
	isTest            bool
}

type editorRow struct {
	// key content key (required).
	key string
	// value jsonpath for backupPolicy.spec.
	jsonpath string
	// updateFunc applies the modified value to backupPolicy (required).
	updateFunc updateBackupPolicyFieldFunc
}

func NewEditBackupPolicyCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := editBackupPolicyOptions{Factory: f, IOStreams: streams, GVR: types.BackupPolicyGVR()}
	cmd := &cobra.Command{
		Use:                   "edit-backup-policy",
		DisableFlagsInUseLine: true,
		Aliases:               []string{"edit-bp"},
		Short:                 "Edit backup policy",
		Example:               editExample,
		ValidArgsFunction:     util.ResourceNameCompletionFunc(f, types.BackupPolicyGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			cmdutil.CheckErr(o.complete(args))
			cmdutil.CheckErr(o.runEditBackupPolicy())
		},
	}
	cmd.Flags().StringArrayVar(&o.values, "set", []string{},
		"set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	return cmd
}

func (o *editBackupPolicyOptions) complete(args []string) error {
	var err error
	if len(args) == 0 {
		return fmt.Errorf("missing backupPolicy name")
	}
	if len(args) > 1 {
		return fmt.Errorf("only support to update one backupPolicy or quote cronExpression")
	}
	o.name = args[0]
	if o.namespace, _, err = o.Factory.ToRawKubeConfigLoader().Namespace(); err != nil {
		return err
	}
	if o.dynamic, err = o.Factory.DynamicClient(); err != nil {
		return err
	}
	updateRepoName := func(backupPolicy *dpv1alpha1.BackupPolicy, targetVal string) error {
		// check if the backup repo exists
		if targetVal != "" {
			_, err := o.dynamic.Resource(types.BackupRepoGVR()).Get(context.Background(), targetVal, metav1.GetOptions{})
			if err != nil {
				return err
			}
		}
		if backupPolicy != nil {
			if targetVal != "" {
				backupPolicy.Spec.BackupRepoName = &targetVal
			} else {
				backupPolicy.Spec.BackupRepoName = nil
			}
		}
		return nil
	}

	o.editContent = []editorRow{
		{
			key:      "backupRepoName",
			jsonpath: "backupRepoName",
			updateFunc: func(backupPolicy *dpv1alpha1.BackupPolicy, targetVal string) error {
				return updateRepoName(backupPolicy, targetVal)
			},
		},
	}
	o.editContentKeyMap = map[string]updateBackupPolicyFieldFunc{}
	for _, v := range o.editContent {
		if v.updateFunc == nil {
			return fmt.Errorf("updateFunc can not be nil")
		}
		o.editContentKeyMap[v.key] = v.updateFunc
	}
	return nil
}

func (o *editBackupPolicyOptions) runEditBackupPolicy() error {
	backupPolicy := &dpv1alpha1.BackupPolicy{}
	key := client.ObjectKey{
		Name:      o.name,
		Namespace: o.namespace,
	}
	err := util.GetResourceObjectFromGVR(types.BackupPolicyGVR(), key, o.dynamic, &backupPolicy)
	if err != nil {
		return err
	}
	if len(o.values) == 0 {
		edited, err := o.runWithEditor(backupPolicy)
		if err != nil {
			return err
		}
		o.values = strings.Split(edited, "\n")
	}
	return o.applyChanges(backupPolicy)
}

func (o *editBackupPolicyOptions) runWithEditor(backupPolicy *dpv1alpha1.BackupPolicy) (string, error) {
	editor := editor.NewDefaultEditor([]string{
		"KUBE_EDITOR",
		"EDITOR",
	})
	contents, err := o.buildEditorContent(backupPolicy)
	if err != nil {
		return "", err
	}
	addHeader := func() string {
		return fmt.Sprintf(`# Please edit the object below. Lines beginning with a '#' will be ignored,
# and an empty file will abort the edit. If an error occurs while saving this file will be
# reopened with the relevant failures.
#
%s
`, *contents)
	}
	if o.isTest {
		// only for testing
		return "", nil
	}
	edited, _, err := editor.LaunchTempFile(fmt.Sprintf("%s-edit-", backupPolicy.Name), "", bytes.NewBufferString(addHeader()))
	if err != nil {
		return "", err
	}
	return string(edited), nil
}

// buildEditorContent builds the editor content.
func (o *editBackupPolicyOptions) buildEditorContent(backPolicy *dpv1alpha1.BackupPolicy) (*string, error) {
	var contents []string
	for _, v := range o.editContent {
		// get the value with jsonpath
		val, err := o.getValueWithJsonpath(backPolicy.Spec, v.jsonpath)
		if err != nil {
			return nil, err
		}
		if val == nil {
			continue
		}
		row := fmt.Sprintf("%s=%s", v.key, *val)
		o.original += row
		contents = append(contents, row)
	}
	result := strings.Join(contents, "\n")
	return &result, nil
}

// getValueWithJsonpath gets the value with jsonpath.
func (o *editBackupPolicyOptions) getValueWithJsonpath(spec dpv1alpha1.BackupPolicySpec, path string) (*string, error) {
	parser := jsonpath.New("edit-backup-policy").AllowMissingKeys(true)
	pathExpression, err := get.RelaxedJSONPathExpression(path)
	if err != nil {
		return nil, err
	}
	if err = parser.Parse(pathExpression); err != nil {
		return nil, err
	}
	values, err := parser.FindResults(spec)
	if err != nil {
		return nil, err
	}
	for _, v := range values {
		if len(v) == 0 {
			continue
		}
		v1 := v[0]
		switch v1.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v1.IsNil() {
				return nil, nil
			}
			val := fmt.Sprintf("%v", v1.Elem())
			return &val, nil
		default:
			val := fmt.Sprintf("%v", v1.Interface())
			return &val, nil
		}
	}
	return nil, nil
}

// applyChanges applies the changes of backupPolicy.
func (o *editBackupPolicyOptions) applyChanges(backupPolicy *dpv1alpha1.BackupPolicy) error {
	for _, v := range o.values {
		row := strings.TrimSpace(v)
		if strings.HasPrefix(row, "#") || row == "" {
			continue
		}
		o.target += row
		arr := strings.Split(row, "=")
		if len(arr) != 2 {
			return fmt.Errorf(`invalid row: %s, format should be "key=value"`, v)
		}
		updateFn, ok := o.editContentKeyMap[arr[0]]
		if !ok {
			return fmt.Errorf(`invalid key: %s`, arr[0])
		}
		arr[1] = strings.Trim(arr[1], `"`)
		arr[1] = strings.Trim(arr[1], `'`)
		if err := updateFn(backupPolicy, arr[1]); err != nil {
			return err
		}
	}
	// if no changes, return.
	if o.original == o.target {
		fmt.Fprintln(o.Out, "updated (no change)")
		return nil
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(backupPolicy)
	if err != nil {
		return err
	}
	if _, err = o.dynamic.Resource(types.BackupPolicyGVR()).Namespace(backupPolicy.Namespace).Update(context.TODO(),
		&unstructured.Unstructured{Object: obj}, metav1.UpdateOptions{}); err != nil {
		return err
	}
	fmt.Fprintln(o.Out, "updated")
	return nil
}

type DescribeBackupPolicyOptions struct {
	namespace string
	dynamic   dynamic.Interface
	Factory   cmdutil.Factory
	client    clientset.Interface

	LabelSelector string
	ClusterNames  []string
	Names         []string

	genericiooptions.IOStreams
}

func (o *DescribeBackupPolicyOptions) Complete() error {
	var err error

	if o.client, err = o.Factory.KubernetesClientSet(); err != nil {
		return err
	}

	if o.dynamic, err = o.Factory.DynamicClient(); err != nil {
		return err
	}

	if o.namespace, _, err = o.Factory.ToRawKubeConfigLoader().Namespace(); err != nil {
		return err
	}
	return nil
}

func (o *DescribeBackupPolicyOptions) Validate() error {
	// must specify one of the cluster name or backup policy name
	if len(o.ClusterNames) == 0 && len(o.Names) == 0 {
		return fmt.Errorf("missing cluster name or backup policy name")
	}

	return nil
}

func (o *DescribeBackupPolicyOptions) Run() error {
	var backupPolicyNameMap = make(map[string]bool)
	for _, name := range o.Names {
		backupPolicyNameMap[name] = true
	}

	backupPolicyList, err := o.dynamic.Resource(types.BackupPolicyGVR()).Namespace(o.namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: o.LabelSelector,
	})
	if err != nil {
		return err
	}

	if len(backupPolicyList.Items) == 0 {
		fmt.Fprintf(o.Out, "No backup policy found\n")
		return nil
	}

	for _, obj := range backupPolicyList.Items {
		backupPolicy := &dpv1alpha1.BackupPolicy{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, backupPolicy); err != nil {
			return err
		}
		isDefault := obj.GetAnnotations()[dptypes.DefaultBackupPolicyAnnotationKey] == "true"
		// if backup policy name is specified, only print the backup policy with the specified name
		if len(o.Names) > 0 && !backupPolicyNameMap[backupPolicy.Name] {
			continue
		}
		// if backup policy name is not specified, only print the default backup policy
		if len(o.Names) == 0 && !isDefault {
			continue
		}
		if err := o.printBackupPolicyObj(backupPolicy); err != nil {
			return err
		}
	}

	return nil
}

func (o *DescribeBackupPolicyOptions) printBackupPolicyObj(obj *dpv1alpha1.BackupPolicy) error {
	printer.PrintLine("Summary:")
	realPrintPairStringToLine("Name", obj.Name)
	realPrintPairStringToLine("Cluster", obj.Labels[constant.AppInstanceLabelKey])
	realPrintPairStringToLine("Namespace", obj.Namespace)
	realPrintPairStringToLine("Default", strconv.FormatBool(obj.Annotations[dptypes.DefaultBackupPolicyAnnotationKey] == "true"))
	if obj.Spec.BackupRepoName != nil {
		realPrintPairStringToLine("Backup Repo Name", *obj.Spec.BackupRepoName)
	}

	printer.PrintLine("\nBackup Methods:")
	p := printer.NewTablePrinter(o.Out)
	p.SetHeader("Name", "ActionSet", "snapshot-volumes")
	for _, v := range obj.Spec.BackupMethods {
		p.AddRow(v.Name, v.ActionSetName, strconv.FormatBool(*v.SnapshotVolumes))
	}
	p.Print()

	return nil
}

func NewDescribeBackupPolicyCmd(f cmdutil.Factory, streams genericiooptions.IOStreams) *cobra.Command {
	o := &DescribeBackupPolicyOptions{
		Factory:   f,
		IOStreams: streams,
	}
	cmd := &cobra.Command{
		Use:               "describe-backup-policy",
		Aliases:           []string{"desc-backup-policy"},
		Short:             "Describe backup policy",
		Example:           describeBackupPolicyExample,
		ValidArgsFunction: util.ResourceNameCompletionFunc(f, types.ClusterGVR()),
		Run: func(cmd *cobra.Command, args []string) {
			o.ClusterNames = args
			o.LabelSelector = util.BuildLabelSelectorByNames(o.LabelSelector, args)
			cmdutil.BehaviorOnFatal(printer.FatalWithRedColor)
			util.CheckErr(o.Complete())
			util.CheckErr(o.Validate())
			util.CheckErr(o.Run())
		},
	}
	cmd.Flags().StringSliceVar(&o.Names, "name", []string{}, "Backup policy name")
	return cmd
}

func (o *DescribeBackupOptions) Complete(args []string) error {
	var err error

	if len(args) == 0 {
		return fmt.Errorf("backup name should be specified")
	}

	o.names = args

	if o.client, err = o.Factory.KubernetesClientSet(); err != nil {
		return err
	}

	if o.dynamic, err = o.Factory.DynamicClient(); err != nil {
		return err
	}

	if o.namespace, _, err = o.Factory.ToRawKubeConfigLoader().Namespace(); err != nil {
		return err
	}
	return nil
}

func (o *DescribeBackupOptions) Run() error {
	for _, name := range o.names {
		backupObj := &dpv1alpha1.Backup{}
		if err := cluster.GetK8SClientObject(o.dynamic, backupObj, o.Gvr, o.namespace, name); err != nil {
			return err
		}
		if err := o.printBackupObj(backupObj); err != nil {
			return err
		}
	}
	return nil
}

func (o *DescribeBackupOptions) printBackupObj(obj *dpv1alpha1.Backup) error {
	targetCluster := obj.Labels[constant.AppInstanceLabelKey]
	printer.PrintLineWithTabSeparator(
		printer.NewPair("Name", obj.Name),
		printer.NewPair("Cluster", targetCluster),
		printer.NewPair("Namespace", obj.Namespace),
	)
	printer.PrintLine("\nSpec:")
	realPrintPairStringToLine("Method", obj.Spec.BackupMethod)
	realPrintPairStringToLine("Policy Name", obj.Spec.BackupPolicyName)

	printer.PrintLine("\nStatus:")
	realPrintPairStringToLine("Phase", string(obj.Status.Phase))
	realPrintPairStringToLine("Total Size", obj.Status.TotalSize)
	if obj.Status.BackupMethod != nil {
		realPrintPairStringToLine("ActionSet Name", obj.Status.BackupMethod.ActionSetName)
	}
	if obj.Status.BackupRepoName != "" {
		realPrintPairStringToLine("Repository", obj.Status.BackupRepoName)
	}
	if obj.Status.PersistentVolumeClaimName != "" {
		realPrintPairStringToLine("PVC Name", obj.Status.PersistentVolumeClaimName)
	}
	if obj.Status.Duration != nil {
		realPrintPairStringToLine("Duration", duration.HumanDuration(obj.Status.Duration.Duration))
	}
	realPrintPairStringToLine("Expiration Time", util.TimeFormat(obj.Status.Expiration))
	realPrintPairStringToLine("Start Time", util.TimeFormat(obj.Status.StartTimestamp))
	realPrintPairStringToLine("Completion Time", util.TimeFormat(obj.Status.CompletionTimestamp))
	// print failure reason, ignore error
	_ = o.enhancePrintFailureReason(obj.Name, obj.Status.FailureReason)

	realPrintPairStringToLine("Path", obj.Status.Path)

	if obj.Status.TimeRange != nil {
		realPrintPairStringToLine("Time Range Start", util.TimeFormat(obj.Status.TimeRange.Start))
		realPrintPairStringToLine("Time Range End", util.TimeFormat(obj.Status.TimeRange.End))
	}

	if len(obj.Status.VolumeSnapshots) > 0 {
		printer.PrintLine("\nVolume Snapshots:")
		for _, v := range obj.Status.VolumeSnapshots {
			realPrintPairStringToLine("Name", v.Name)
			realPrintPairStringToLine("Content Name", v.ContentName)
			realPrintPairStringToLine("Volume Name:", v.VolumeName)
			realPrintPairStringToLine("Size", v.Size)
		}
	}

	// get all events about backup
	events, err := o.client.CoreV1().Events(o.namespace).Search(scheme.Scheme, obj)
	if err != nil {
		return err
	}

	// print the warning events
	printer.PrintAllWarningEvents(events, o.Out)

	return nil
}

func realPrintPairStringToLine(name, value string, spaceCount ...int) {
	if value != "" {
		printer.PrintPairStringToLine(name, value, spaceCount...)
	}
}

// print the pod error logs if failure reason has occurred
// TODO: the failure reason should be improved in the backup controller
func (o *DescribeBackupOptions) enhancePrintFailureReason(backupName, failureReason string, spaceCount ...int) error {
	if failureReason == "" {
		return nil
	}
	ctx := context.Background()
	// get the latest job log details.
	labels := fmt.Sprintf("%s=%s",
		dptypes.BackupNameLabelKey, backupName,
	)
	jobList, err := o.client.BatchV1().Jobs("").List(ctx, metav1.ListOptions{LabelSelector: labels})
	if err != nil {
		return err
	}
	var failedJob *batchv1.Job
	for _, i := range jobList.Items {
		if i.Status.Failed > 0 {
			failedJob = &i
			break
		}
	}
	if failedJob != nil {
		podLabels := fmt.Sprintf("%s=%s",
			"controller-uid", failedJob.UID,
		)
		podList, err := o.client.CoreV1().Pods(failedJob.Namespace).List(ctx, metav1.ListOptions{LabelSelector: podLabels})
		if err != nil {
			return err
		}
		if len(podList.Items) > 0 {
			tailLines := int64(5)
			req := o.client.CoreV1().
				Pods(podList.Items[0].Namespace).
				GetLogs(podList.Items[0].Name, &corev1.PodLogOptions{TailLines: &tailLines})
			data, err := req.DoRaw(ctx)
			if err != nil {
				return err
			}
			failureReason = fmt.Sprintf("%s\n pod %s error logs:\n%s",
				failureReason, podList.Items[0].Name, string(data))
		}
	}
	printer.PrintPairStringToLine("Failure Reason", failureReason, spaceCount...)

	return nil
}
