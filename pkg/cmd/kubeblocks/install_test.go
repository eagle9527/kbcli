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

package kubeblocks

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	clientfake "k8s.io/client-go/rest/fake"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"

	"github.com/apecloud/kbcli/pkg/testing"
	"github.com/apecloud/kbcli/pkg/util"
	"github.com/apecloud/kbcli/pkg/util/helm"
	"github.com/apecloud/kbcli/version"
)

const namespace = "test"

var _ = Describe("kubeblocks install", func() {
	var (
		cmd     *cobra.Command
		streams genericiooptions.IOStreams
		tf      *cmdtesting.TestFactory
	)

	BeforeEach(func() {
		streams, _, _, _ = genericiooptions.NewTestIOStreams()
		tf = cmdtesting.NewTestFactory().WithNamespace(namespace)
		tf.Client = &clientfake.RESTClient{}
	})

	AfterEach(func() {
		tf.Cleanup()
	})

	It("check install", func() {
		var cfg string
		cmd = newInstallCmd(tf, streams)
		Expect(cmd).ShouldNot(BeNil())
		Expect(cmd.HasSubCommands()).Should(BeFalse())

		o := &InstallOptions{
			Options: Options{
				IOStreams: streams,
			},
		}

		By("command without kubeconfig flag")
		Expect(o.Complete(tf, cmd)).Should(HaveOccurred())

		cmd.Flags().StringVar(&cfg, "kubeconfig", "", "Path to the kubeconfig file to use for CLI requests.")
		cmd.Flags().StringVar(&cfg, "context", "", "The name of the kubeconfig context to use.")
		Expect(o.Complete(tf, cmd)).To(Succeed())
		Expect(o.HelmCfg).ShouldNot(BeNil())
		Expect(o.Namespace).To(Equal("test"))
	})

	It("run install", func() {
		o := &InstallOptions{
			Options: Options{
				IOStreams: streams,
				HelmCfg:   helm.NewFakeConfig(namespace),
				Client:    testing.FakeClientSet(),
				Dynamic:   testing.FakeDynamicClient(),
			},
			Version:         version.DefaultKubeBlocksVersion,
			CreateNamespace: true,
		}
		Expect(o.Install()).Should(HaveOccurred())
		Expect(o.ValueOpts.Values).Should(HaveLen(0))
		Expect(o.installChart()).Should(HaveOccurred())
		o.printNotes()
	})

	It("checkVersion", func() {
		o := &InstallOptions{
			Options: Options{
				IOStreams: genericiooptions.NewTestIOStreamsDiscard(),
				Client:    testing.FakeClientSet(),
			},
			Check: true,
		}
		By("kubernetes version is empty")
		v := util.Version{}
		Expect(o.checkVersion(v).Error()).Should(ContainSubstring("failed to get kubernetes version"))

		By("kubernetes is provided by cloud provider")
		v.Kubernetes = "v1.25.0-eks"
		Expect(o.checkVersion(v)).Should(Succeed())

		By("kubernetes is not provided by cloud provider")
		v.Kubernetes = "v1.25.0"
		Expect(o.checkVersion(v)).Should(Succeed())
	})

	It("CompleteInstallOptions test", func() {
		o := &InstallOptions{
			Options: Options{
				IOStreams: streams,
				HelmCfg:   helm.NewFakeConfig(namespace),
				Client:    testing.FakeClientSet(),
				Dynamic:   testing.FakeDynamicClient(),
			},
			Version:         version.DefaultKubeBlocksVersion,
			CreateNamespace: true,
		}
		Expect(o.TolerationsRaw).Should(BeNil())
		Expect(o.ValueOpts.JSONValues).Should(BeNil())
		Expect(o.CompleteInstallOptions()).ShouldNot(HaveOccurred())
		Expect(o.TolerationsRaw).Should(Equal([]string{defaultTolerationsForInstallation}))
		Expect(o.ValueOpts.JSONValues).ShouldNot(BeNil())
	})
})
