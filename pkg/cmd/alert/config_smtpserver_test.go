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

package alert

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	clientfake "k8s.io/client-go/rest/fake"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"

	"github.com/apecloud/kbcli/pkg/testing"
)

var mockBaseOptionsWithoutGlobal = func(s genericiooptions.IOStreams) baseOptions {
	o := baseOptions{IOStreams: s}
	alertManagerConfig := `
    receivers:
    - name: default-receiver
    - name: receiver-7pb52
      webhook_configs:
      - max_alerts: 10
        url: http://kubeblocks-webhook-adaptor-config.default:5001/api/v1/notify/receiver-7pb52
    route:
      group_interval: 30s
      group_wait: 5s
      receiver: default-receiver
      repeat_interval: 10m
      routes:
      - continue: true
        matchers:
        - app_kubernetes_io_instance=~a|b|c
        - severity=~info|warning
        receiver: receiver-7pb52`
	webhookAdaptorConfig := `
    receivers:
    - name: receiver-7pb52
      params:
        url: https://oapi.dingtalk.com/robot/send?access_token=123456
      type: dingtalk-webhook`
	alertCM := mockConfigmap(alertConfigmapName, alertConfigFileName, alertManagerConfig)
	webhookAdaptorCM := mockConfigmap(webhookAdaptorConfigmapName, webhookAdaptorFileName, webhookAdaptorConfig)
	o.alertConfigMap = alertCM
	o.webhookConfigMap = webhookAdaptorCM
	return o
}

var _ = Describe("config smtpserver", func() {
	var f *cmdtesting.TestFactory
	var s genericiooptions.IOStreams

	BeforeEach(func() {
		f = cmdtesting.NewTestFactory()
		f.Client = &clientfake.RESTClient{}
		s, _, _, _ = genericiooptions.NewTestIOStreams()
	})

	AfterEach(func() {
		f.Cleanup()
	})

	It("validate", func() {
		By("nothing to be input, should fail")
		o := &configSMTPServerOptions{baseOptions: baseOptions{IOStreams: s}}
		Expect(o.validate()).Should(HaveOccurred())

		By("set smtp-from, do not set smtp-smarthost, should fail")
		Expect(o.validate()).Should(HaveOccurred())

		By("set smtp-from, set smtp-smarthost, do not set smtp-auth-username, should fail")
		Expect(o.validate()).Should(HaveOccurred())

		By("set smtp-from, set smtp-smarthost, set smtp-auth-username, do not set smtp-auth-password, should fail")
		Expect(o.validate()).Should(HaveOccurred())

		By("set smtp-from, set smtp-smarthost, set smtp-auth-username, set smtp-auth-password, should pass")
		Expect(o.validate()).ShouldNot(Succeed())
	})

	It("set global", func() {
		o := configSMTPServerOptions{baseOptions: mockBaseOptionsWithoutGlobal(s)}
		o.smtpFrom = "user@kubeblocks.io"
		o.smtpSmarthost = "smtp.feishu.cn:587"
		o.smtpAuthUsername = "admin@kubeblocks.io"
		o.smtpAuthPassword = "123456abc"
		o.smtpAuthIdentity = "admin@kubeblocks.io"
		o.client = testing.FakeClientSet(o.alertConfigMap, o.webhookConfigMap)
		Expect(o.validate()).Should(Succeed())
		Expect(o.run()).Should(Succeed())
	})
})
