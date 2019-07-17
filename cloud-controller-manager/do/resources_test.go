/*
Copyright 2017 DigitalOcean

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package do

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalocean/godo"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

type serviceBuilder struct {
	idx                int
	isTypeLoadBalancer bool
	loadBalancerID     string
}

func newSvcBuilder(idx int) *serviceBuilder {
	return &serviceBuilder{
		idx: idx,
	}
}

func (sb *serviceBuilder) setTypeLoadBalancer(isTypeLoadBalancer bool) *serviceBuilder {
	sb.isTypeLoadBalancer = isTypeLoadBalancer
	return sb
}

func (sb *serviceBuilder) setLoadBalancerID(id string) *serviceBuilder {
	sb.loadBalancerID = id
	return sb
}

func (sb *serviceBuilder) build() *v1.Service {
	rep := func(num int) string {
		return strings.Repeat(strconv.Itoa(sb.idx), num)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("service%d", sb.idx),
			Namespace:   corev1.NamespaceDefault,
			UID:         types.UID(fmt.Sprintf("%s-%s-%s-%s-%s", rep(7), rep(4), rep(4), rep(4), rep(12))),
			Annotations: map[string]string{},
		},
	}
	if sb.isTypeLoadBalancer {
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	}
	if sb.loadBalancerID != "" {
		svc.Annotations[annoDOLoadBalancerID] = sb.loadBalancerID
	}

	return svc
}

func lbName(idx int) string {
	svc := newSvcBuilder(idx).build()
	return getDefaultLoadBalancerName(svc)
}

func createLBSvc(idx int) *corev1.Service {
	return newSvcBuilder(idx).setTypeLoadBalancer(true).build()
}

func TestResources_Droplets(t *testing.T) {
	droplets := []*godo.Droplet{
		{ID: 1}, {ID: 2},
	}
	resources := &resources{
		dropletIDMap: map[int]*godo.Droplet{
			droplets[0].ID: droplets[0],
			droplets[1].ID: droplets[1],
		},
	}

	foundDroplets := resources.Droplets()
	// order found droplets by id so we can compare
	sort.Slice(foundDroplets, func(a, b int) bool { return foundDroplets[a].ID < foundDroplets[b].ID })
	if want, got := droplets, foundDroplets; !reflect.DeepEqual(want, got) {
		t.Errorf("incorrect droplets\nwant: %#v\n got: %#v", want, got)
	}
}

func TestResources_SyncDroplet(t *testing.T) {
	tests := []struct {
		name              string
		dropletsSvc       godo.DropletsService
		initialResources  *resources
		expectedResources *resources
		err               error
	}{
		{
			name: "happy path",
			dropletsSvc: &fakeDropletService{
				getFunc: func(ctx context.Context, id int) (*godo.Droplet, *godo.Response, error) {
					return &godo.Droplet{ID: 1, Name: "updated-one"}, newFakeOKResponse(), nil
				},
			},
			initialResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "one"}},
				dropletNameMap: map[string]*godo.Droplet{"one": {ID: 1, Name: "one"}},
			},
			expectedResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "updated-one"}},
				dropletNameMap: map[string]*godo.Droplet{"updated-one": {ID: 1, Name: "updated-one"}},
			},
			err: nil,
		},
		{
			name: "error",
			dropletsSvc: &fakeDropletService{
				getFunc: func(ctx context.Context, id int) (*godo.Droplet, *godo.Response, error) {
					return nil, newFakeNotOKResponse(), errors.New("fail")
				},
			},
			initialResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "one"}},
				dropletNameMap: map[string]*godo.Droplet{"updated-one": {ID: 1, Name: "one"}},
			},
			expectedResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "one"}},
				dropletNameMap: map[string]*godo.Droplet{"one": {ID: 1, Name: "one"}},
			},
			err: errors.New("fail"),
		},
		{
			name: "droplet not found",
			dropletsSvc: &fakeDropletService{
				getFunc: func(ctx context.Context, id int) (*godo.Droplet, *godo.Response, error) {
					return nil, newFakeResponse(http.StatusNotFound), errors.New("not found")
				},
			},
			initialResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "one"}},
				dropletNameMap: map[string]*godo.Droplet{"one": {ID: 1, Name: "one"}},
			},
			expectedResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{},
				dropletNameMap: map[string]*godo.Droplet{},
			},
			err: nil,
		},
		{
			name: "new droplet",
			dropletsSvc: &fakeDropletService{
				getFunc: func(ctx context.Context, id int) (*godo.Droplet, *godo.Response, error) {
					return &godo.Droplet{ID: 1, Name: "one"}, newFakeOKResponse(), nil
				},
			},
			initialResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{},
				dropletNameMap: map[string]*godo.Droplet{},
			},
			expectedResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "one"}},
				dropletNameMap: map[string]*godo.Droplet{"one": {ID: 1, Name: "one"}},
			},
			err: nil,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &godo.Client{
				Droplets: test.dropletsSvc,
			}
			fakeResources := newResources("", "", client)
			fakeResources.dropletIDMap = test.initialResources.dropletIDMap
			fakeResources.dropletNameMap = test.initialResources.dropletNameMap

			err := fakeResources.SyncDroplet(context.Background(), 1)
			if test.err != nil {
				if !reflect.DeepEqual(err, test.err) {
					t.Errorf("incorrect err\nwant: %#v\n got: %#v", test.err, err)
				}
				return
			}
			if err != nil {
				t.Errorf("did not expect err but got: %s", err)
				return
			}

			if want, got := test.expectedResources.dropletIDMap, fakeResources.dropletIDMap; !reflect.DeepEqual(want, got) {
				t.Errorf("incorrect droplet id map\nwant: %#v\n got: %#v", want, got)
			}
			if want, got := test.expectedResources.dropletNameMap, fakeResources.dropletNameMap; !reflect.DeepEqual(want, got) {
				t.Errorf("incorrect droplet name map\nwant: %#v\n got: %#v", want, got)
			}
		})
	}
}

func TestResources_SyncDroplets(t *testing.T) {
	tests := []struct {
		name              string
		dropletsSvc       godo.DropletsService
		expectedResources *resources
		err               error
	}{
		{
			name: "happy path",
			dropletsSvc: &fakeDropletService{
				listFunc: func(ctx context.Context, opt *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
					return []godo.Droplet{{ID: 2, Name: "two"}}, newFakeOKResponse(), nil
				},
			},
			expectedResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{2: {ID: 2, Name: "two"}},
				dropletNameMap: map[string]*godo.Droplet{"two": {ID: 2, Name: "two"}},
			},
			err: nil,
		},
		{
			name: "droplets svc failure",
			dropletsSvc: &fakeDropletService{
				listFunc: func(ctx context.Context, opt *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
					return nil, newFakeNotOKResponse(), errors.New("droplets svc fail")
				},
			},
			expectedResources: &resources{
				dropletIDMap:   map[int]*godo.Droplet{1: {ID: 1, Name: "one"}},
				dropletNameMap: map[string]*godo.Droplet{"one": {ID: 1, Name: "one"}},
			},
			err: errors.New("droplets svc fail"),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := &godo.Client{
				Droplets: test.dropletsSvc,
			}
			fakeResources := newResources("", "", client)
			fakeResources.UpdateDroplets([]godo.Droplet{
				{ID: 1, Name: "one"},
			})

			err := fakeResources.SyncDroplets(context.Background())
			if test.err != nil {
				if !reflect.DeepEqual(err, test.err) {
					t.Errorf("incorrect err\nwant: %#v\n got: %#v", test.err, err)
				}
				return
			}
			if err != nil {
				t.Errorf("did not expect err but got: %s", err)
				return
			}

			if want, got := test.expectedResources.dropletIDMap, fakeResources.dropletIDMap; !reflect.DeepEqual(want, got) {
				t.Errorf("incorrect droplet id map\nwant: %#v\n got: %#v", want, got)
			}
			if want, got := test.expectedResources.dropletNameMap, fakeResources.dropletNameMap; !reflect.DeepEqual(want, got) {
				t.Errorf("incorrect droplet name map\nwant: %#v\n got: %#v", want, got)
			}
		})
	}
}

type recordingSyncer struct {
	*tickerSyncer

	synced map[string]int
	mutex  sync.Mutex
	stopOn int
	stopCh chan struct{}
}

func newRecordingSyncer(stopOn int, stopCh chan struct{}) *recordingSyncer {
	return &recordingSyncer{
		tickerSyncer: &tickerSyncer{},
		synced:       make(map[string]int),
		stopOn:       stopOn,
		stopCh:       stopCh,
	}
}

func (s *recordingSyncer) Sync(name string, period time.Duration, stopCh <-chan struct{}, fn func() error) {
	recordingFn := func() error {
		s.mutex.Lock()
		defer s.mutex.Unlock()

		count, _ := s.synced[name]
		s.synced[name] = count + 1

		if len(s.synced) == s.stopOn {
			close(s.stopCh)
		}

		return fn()
	}

	s.tickerSyncer.Sync(name, period, stopCh, recordingFn)
}

var (
	clusterID    = "0caf4c4e-e835-4a05-9ee8-5726bb66ab07"
	clusterIDTag = buildK8sTag(clusterID)
)

func TestResourcesController_Run(t *testing.T) {
	gclient := &godo.Client{
		Droplets: &fakeDropletService{
			listFunc: func(ctx context.Context, opt *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
				return []godo.Droplet{{ID: 2, Name: "two"}}, newFakeOKResponse(), nil
			},
		},
		LoadBalancers: &fakeLBService{
			listFn: func(context.Context, *godo.ListOptions) ([]godo.LoadBalancer, *godo.Response, error) {
				return []godo.LoadBalancer{{ID: "2", Name: "two"}}, newFakeOKResponse(), nil
			},
		},
	}
	fakeResources := newResources(clusterID, "", gclient)
	kclient := fake.NewSimpleClientset()
	inf := informers.NewSharedInformerFactory(kclient, 0)

	res := NewResourcesController(fakeResources, inf.Core().V1().Services(), kclient)
	stop := make(chan struct{})
	syncer := newRecordingSyncer(2, stop)
	res.syncer = syncer

	res.Run(stop)

	select {
	case <-stop:
		// No-op: test succeeded
	case <-time.After(3 * time.Second):
		// Terminate goroutines just in case.
		close(stop)
		t.Errorf("resources calls: %d tags calls: %d", syncer.synced["resources syncer"], syncer.synced["tags syncer"])
	}
}

func TestResourcesController_SyncTags(t *testing.T) {
	testcases := []struct {
		name        string
		services    []*corev1.Service
		lbs         []godo.LoadBalancer
		tagSvc      *fakeTagsService
		errMsg      string
		tagRequests []*godo.TagResourcesRequest
	}{
		{
			name:     "no matching services",
			services: []*corev1.Service{createLBSvc(1)},
			lbs: []godo.LoadBalancer{
				{ID: "1", Name: lbName(2)},
			},
		},
		{
			name: "service without LoadBalancer type",
			services: []*corev1.Service{
				newSvcBuilder(1).setTypeLoadBalancer(false).build(),
			},
			lbs: []godo.LoadBalancer{
				{ID: "1", Name: lbName(1)},
			},
		},
		{
			name:     "unrecoverable resource tagging error",
			services: []*corev1.Service{createLBSvc(1)},
			lbs: []godo.LoadBalancer{
				{ID: "1", Name: lbName(1)},
			},
			tagSvc: newFakeTagsServiceWithFailure(0, errors.New("no tagging for you")),
			errMsg: "no tagging for you",
		},
		{
			name:     "unrecoverable resource creation error",
			services: []*corev1.Service{createLBSvc(1)},
			lbs: []godo.LoadBalancer{
				{ID: "1", Name: lbName(1)},
			},
			tagSvc: newFakeTagsServiceWithFailure(1, errors.New("no tag creating for you")),
			errMsg: "no tag creating for you",
		},
		{
			name: "success on first resource tagging",
			services: []*corev1.Service{
				createLBSvc(1),
			},
			lbs: []godo.LoadBalancer{
				{Name: lbName(1)},
			},
			tagSvc: newFakeTagsService(clusterIDTag),
		},
		{
			name: "multiple tags",
			services: []*corev1.Service{
				createLBSvc(1),
				createLBSvc(2),
			},
			lbs: []godo.LoadBalancer{
				{ID: "1", Name: lbName(1)},
				{ID: "2", Name: lbName(2)},
			},
			tagSvc: newFakeTagsService(clusterIDTag),
			tagRequests: []*godo.TagResourcesRequest{
				{
					Resources: []godo.Resource{
						{
							ID:   "1",
							Type: godo.LoadBalancerResourceType,
						},
						{
							ID:   "2",
							Type: godo.LoadBalancerResourceType,
						},
					},
				},
			},
		},
		{
			name: "success on second resource tagging",
			services: []*corev1.Service{
				createLBSvc(1),
			},
			lbs: []godo.LoadBalancer{
				{ID: "1", Name: lbName(1)},
			},
			tagSvc: newFakeTagsService(),
		},
		{
			name: "found LB resource by ID annotation",
			services: []*corev1.Service{
				newSvcBuilder(1).setTypeLoadBalancer(true).setLoadBalancerID("f7968b52-4ed9-4a16-af8b-304253f04e20").build(),
			},
			lbs: []godo.LoadBalancer{
				{Name: "renamed-lb"},
			},
			tagSvc: newFakeTagsService(clusterIDTag),
			tagRequests: []*godo.TagResourcesRequest{
				{
					Resources: []godo.Resource{
						{
							ID:   "f7968b52-4ed9-4a16-af8b-304253f04e20",
							Type: godo.LoadBalancerResourceType,
						},
					},
				},
			},
		},
	}

	for _, test := range testcases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			gclient := &godo.Client{
				Droplets: nil,
				LoadBalancers: &fakeLBService{
					listFn: func(context.Context, *godo.ListOptions) ([]godo.LoadBalancer, *godo.Response, error) {
						return test.lbs, newFakeOKResponse(), nil
					},
				},
			}

			fakeResources := newResources("", "", gclient)
			fakeTagsService := test.tagSvc
			if fakeTagsService == nil {
				fakeTagsService = newFakeTagsServiceWithFailure(0, errors.New("tags service not configured, should probably not have been called"))
			}

			gclient.Tags = fakeTagsService
			kclient := fake.NewSimpleClientset()

			for _, svc := range test.services {
				_, err := kclient.CoreV1().Services(corev1.NamespaceDefault).Create(svc)
				if err != nil {
					t.Fatalf("failed to create service: %s", err)
				}
			}

			sharedInformer := informers.NewSharedInformerFactory(kclient, 0)
			res := NewResourcesController(fakeResources, sharedInformer.Core().V1().Services(), kclient)
			sharedInformer.Start(nil)
			sharedInformer.WaitForCacheSync(nil)

			wantErr := test.errMsg != ""
			err := res.syncTags()
			if wantErr != (err != nil) {
				t.Fatalf("got error %q, want error: %t", err, wantErr)
			}

			if wantErr && !strings.Contains(err.Error(), test.errMsg) {
				t.Errorf("error message %q does not contain %q", err.Error(), test.errMsg)
			}

			if test.tagRequests != nil {
				// We need to sort request resources for reliable test
				// assertions as informer's List() ordering is indeterministic.
				for _, tagReq := range fakeTagsService.tagRequests {
					sort.SliceStable(tagReq.Resources, func(i, j int) bool {
						return tagReq.Resources[i].ID < tagReq.Resources[j].ID
					})
				}

				if !reflect.DeepEqual(test.tagRequests, fakeTagsService.tagRequests) {
					want, _ := json.Marshal(test.tagRequests)
					got, _ := json.Marshal(fakeTagsService.tagRequests)
					t.Errorf("want tagRequests %s, got %s", want, got)
				}
			}
		})
	}
}
