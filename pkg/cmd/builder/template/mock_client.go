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

package template

import (
	"context"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cfgcore "github.com/apecloud/kubeblocks/pkg/configuration/core"
)

type ResourceMatcher = func(obj runtime.Object) bool
type Handler = func(obj runtime.Object) error

type ResourceHandler struct {
	Matcher []ResourceMatcher
	Handler Handler
}

type mockClient struct {
	objects        map[client.ObjectKey]client.Object
	kindObjectList map[string][]runtime.Object

	hander *ResourceHandler
}

func newMockClient(objs []client.Object) *mockClient {
	return &mockClient{
		objects:        fromObjects(objs),
		kindObjectList: splitRuntimeObject(objs),
	}
}

func (m *mockClient) SetResourceHandler(resourceHandler *ResourceHandler) {
	m.hander = resourceHandler
}

func fromObjects(objs []client.Object) map[client.ObjectKey]client.Object {
	r := make(map[client.ObjectKey]client.Object)
	for _, obj := range objs {
		if obj != nil {
			r[client.ObjectKeyFromObject(obj)] = obj
		}
	}
	return r
}

func (m *mockClient) AppendMockObjects(obj client.Object) {
	objKey := client.ObjectKeyFromObject(obj)
	if _, ok := m.objects[objKey]; ok {
		return
	}
	m.objects[objKey] = obj
}

func splitRuntimeObject(objects []client.Object) map[string][]runtime.Object {
	r := make(map[string][]runtime.Object)
	for _, object := range objects {
		kind := object.GetObjectKind().GroupVersionKind().Kind
		if _, ok := r[kind]; !ok {
			r[kind] = make([]runtime.Object, 0)
		}
		r[kind] = append(r[kind], object)
	}
	return r
}

func (m *mockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	objKey := key
	if object, ok := m.objects[objKey]; ok {
		SetGetReturnedObject(obj, object)
		return nil
	}
	objKey.Namespace = ""
	if object, ok := m.objects[objKey]; ok {
		SetGetReturnedObject(obj, object)
		return nil
	}
	return apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("mock_resource").GroupResource(), key.String())
}

func (m *mockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	r := m.kindObjectList[list.GetObjectKind().GroupVersionKind().Kind]
	if r != nil {
		return SetListReturnedObjects(list, r)
	}
	return nil
}

func (m *mockClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if m.hander == nil || len(m.hander.Matcher) == 0 {
		return nil
	}

	for _, matcher := range m.hander.Matcher {
		if !matcher(obj) {
			return nil
		}
	}
	return m.hander.Handler(obj)
}

func (m *mockClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return nil
}

func (m *mockClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return nil
}

func (m *mockClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return nil
}

func (m *mockClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return cfgcore.MakeError("not support")
}

func (m *mockClient) Status() client.SubResourceWriter {
	panic("implement me")
}

func (m *mockClient) SubResource(subResource string) client.SubResourceClient {
	panic("implement me")
}

func (m *mockClient) Scheme() *runtime.Scheme {
	panic("implement me")
}

func (m *mockClient) RESTMapper() meta.RESTMapper {
	panic("implement me")
}

func (m *mockClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	panic("implement me")
}

func (m *mockClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	panic("implement me")
}

func SetGetReturnedObject(out client.Object, expectedObj client.Object) {
	outVal := reflect.ValueOf(out)
	objVal := reflect.ValueOf(expectedObj)
	reflect.Indirect(outVal).Set(reflect.Indirect(objVal))
}

func SetListReturnedObjects(list client.ObjectList, objects []runtime.Object) error {
	return meta.SetList(list, objects)
}
