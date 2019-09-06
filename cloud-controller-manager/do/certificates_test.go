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
	"fmt"
	"reflect"
	"testing"

	"github.com/digitalocean/godo"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type kvCertService struct {
	store    map[string]*godo.Certificate
	getFn    func(context.Context, string) (*godo.Certificate, *godo.Response, error)
	listFn   func(context.Context, *godo.ListOptions) ([]godo.Certificate, *godo.Response, error)
	createFn func(context.Context, *godo.CertificateRequest) (*godo.Certificate, *godo.Response, error)
	deleteFn func(ctx context.Context, lbID string) (*godo.Response, error)
}

func (f *kvCertService) Get(ctx context.Context, certID string) (*godo.Certificate, *godo.Response, error) {
	return f.getFn(ctx, certID)
}

func (f *kvCertService) List(ctx context.Context, listOpts *godo.ListOptions) ([]godo.Certificate, *godo.Response, error) {
	return f.listFn(ctx, listOpts)
}

func (f *kvCertService) Create(ctx context.Context, crtr *godo.CertificateRequest) (*godo.Certificate, *godo.Response, error) {
	return f.createFn(ctx, crtr)
}

func (f *kvCertService) Delete(ctx context.Context, certID string) (*godo.Response, error) {
	return f.deleteFn(ctx, certID)
}

func newKVCertService(store map[string]*godo.Certificate) kvCertService {
	return kvCertService{
		store: store,
		getFn: func(ctx context.Context, id string) (*godo.Certificate, *godo.Response, error) {
			lb, ok := store[id]
			if ok {
				return lb, newFakeOKResponse(), nil
			}
			return nil, newFakeNotOKResponse(), newFakeNotFoundErrorResponse()
		},
		listFn: func(context.Context, *godo.ListOptions) ([]godo.Certificate, *godo.Response, error) {
			response := make([]godo.Certificate, len(store))
			for _, cert := range store {
				response = append(response, *cert)
			}
			return response, newFakeOKResponse(), nil
		},
	}
}

func createServiceAndCert(lbID, certID, certType string) (*v1.Service, *godo.Certificate) {
	c := &godo.Certificate{
		ID:   certID,
		Type: certType,
	}
	s := createServiceWithCert(lbID, certID)
	return s, c
}

func createServiceWithCert(lbID, certID string) *v1.Service {
	s := createService(lbID)
	s.Annotations[annDOCertificateID] = certID
	return s
}

func createService(lbID string) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  "foobar123",
			Annotations: map[string]string{
				annDOProtocol:        "http",
				annoDOLoadBalancerID: lbID,
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:     "test",
					Protocol: "TCP",
					Port:     int32(443),
					NodePort: int32(30000),
				},
			},
		},
	}
}

func Test_LBaaSCertificateScenarios(t *testing.T) {
	testcases := []struct {
		name                  string
		setupFn               func(fakeLBService, kvCertService) *v1.Service
		expectedServiceCertID string
		expectedLBCertID      string
		err                   error
	}{
		{
			name: "default test values, tls not enabled",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb := createLB()
				lbService.store[lb.ID] = lb
				return createService(lb.ID)
			},
		},

		// lets_encrypt test cases
		{
			name: "[letsencrypt] LB cert ID and service cert ID match ",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeLetsEncrypt)
				lbService.store[lb.ID] = lb
				certService.store[cert.ID] = cert
				return createServiceWithCert(lb.ID, cert.ID)
			},
			expectedServiceCertID: "test-cert-id",
			expectedLBCertID:      "test-cert-id",
		},
		{
			name: "[letsencrypt] LB cert ID and service cert ID match and correspond to non-existent cert",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeLetsEncrypt)
				lbService.store[lb.ID] = lb
				return createServiceWithCert(lb.ID, cert.ID)
			},
			expectedLBCertID:      "test-cert-id",
			expectedServiceCertID: "test-cert-id",
			err:                   fmt.Errorf("the %q service annotation refers to nonexistent DO Certificate %q", annDOCertificateID, "test-cert-id"),
		},
		{
			name: "[letsencrypt] LB cert ID and service cert ID differ and both certs exist",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeLetsEncrypt)
				lbService.store[lb.ID] = lb
				certService.store[cert.ID] = cert

				service, cert := createServiceAndCert(lb.ID, "service-cert-id", certTypeLetsEncrypt)
				certService.store[cert.ID] = cert
				return service
			},
			expectedServiceCertID: "test-cert-id",
			expectedLBCertID:      "test-cert-id",
		},
		{
			name: "[letsencrypt] LB cert ID exists and service cert ID does not",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeLetsEncrypt)
				lbService.store[lb.ID] = lb
				certService.store[cert.ID] = cert
				service, _ := createServiceAndCert(lb.ID, "meow", certTypeLetsEncrypt)
				return service
			},
			expectedServiceCertID: "test-cert-id",
			expectedLBCertID:      "test-cert-id",
		},
		{
			name: "[lets_encrypt] LB cert ID does not exit and service cert ID does",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, _ := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeLetsEncrypt)
				lbService.store[lb.ID] = lb

				service, cert := createServiceAndCert(lb.ID, "service-cert-id", certTypeLetsEncrypt)
				certService.store[cert.ID] = cert
				return service
			},
			expectedServiceCertID: "service-cert-id",
			expectedLBCertID:      "service-cert-id",
		},

		// custom test cases
		{
			name: "[custom] LB cert ID and service cert ID match ",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeCustom)
				lbService.store[lb.ID] = lb
				certService.store[cert.ID] = cert
				return createServiceWithCert(lb.ID, cert.ID)
			},
			expectedServiceCertID: "test-cert-id",
			expectedLBCertID:      "test-cert-id",
		},
		{
			name: "[custom] LB cert ID and service cert ID match and correspond to non-existent cert",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeCustom)
				lbService.store[lb.ID] = lb
				return createServiceWithCert(lb.ID, cert.ID)
			},
			expectedLBCertID:      "test-cert-id",
			expectedServiceCertID: "test-cert-id",
			err:                   fmt.Errorf("the %q service annotation refers to nonexistent DO Certificate %q", annDOCertificateID, "test-cert-id"),
		},
		{
			name: "[custom] LB cert ID and service cert ID differ and both certs exist",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeCustom)
				lbService.store[lb.ID] = lb
				certService.store[cert.ID] = cert

				service, cert := createServiceAndCert(lb.ID, "service-cert-id", certTypeCustom)
				certService.store[cert.ID] = cert
				return service
			},
			expectedServiceCertID: "service-cert-id",
			expectedLBCertID:      "service-cert-id",
		},
		{
			name: "[custom] LB cert ID exists and service cert ID does not",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, cert := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeCustom)
				lbService.store[lb.ID] = lb
				certService.store[cert.ID] = cert

				service, _ := createServiceAndCert(lb.ID, "service-cert-id", certTypeCustom)
				return service
			},
			expectedServiceCertID: "service-cert-id",
			expectedLBCertID:      "test-cert-id",
			err:                   fmt.Errorf("the %q service annotation refers to nonexistent DO Certificate %q", annDOCertificateID, "service-cert-id"),
		},
		{
			name: "[custom] LB cert ID does not exit and service cert ID does",
			setupFn: func(lbService fakeLBService, certService kvCertService) *v1.Service {
				lb, _ := createHTTPSLB(443, 30000, "test-lb-id", "test-cert-id", certTypeCustom)
				lbService.store[lb.ID] = lb

				service, cert := createServiceAndCert(lb.ID, "service-cert-id", certTypeCustom)
				certService.store[cert.ID] = cert
				return service
			},
			expectedServiceCertID: "service-cert-id",
			expectedLBCertID:      "service-cert-id",
		},
	}

	nodes := []*v1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-2",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-3",
			},
		},
	}
	droplets := []godo.Droplet{
		{
			ID:   100,
			Name: "node-1",
		},
		{
			ID:   101,
			Name: "node-2",
		},
		{
			ID:   102,
			Name: "node-3",
		},
	}
	fakeDroplet := fakeDropletService{
		listFunc: func(context.Context, *godo.ListOptions) ([]godo.Droplet, *godo.Response, error) {
			return droplets, newFakeOKResponse(), nil
		},
	}

	for _, tc := range testcases {
		tc := tc
		for _, methodName := range []string{"EnsureLoadBalancer", "UpdateLoadBalancer"} {
			t.Run(tc.name+"_"+methodName, func(t *testing.T) {
				lbStore := make(map[string]*godo.LoadBalancer)
				lbService := newKVLBService(lbStore)
				certStore := make(map[string]*godo.Certificate)
				certService := newKVCertService(certStore)
				service := tc.setupFn(lbService, certService)

				fakeClient := newFakeClient(&fakeDroplet, &lbService, &certService)
				fakeResources := newResources("", "", fakeClient)
				fakeResources.kclient = fake.NewSimpleClientset()
				if _, err := fakeResources.kclient.CoreV1().Services(service.Namespace).Create(service); err != nil {
					t.Fatalf("failed to add service to fake client: %s", err)
				}

				lb := &loadBalancers{
					resources:         fakeResources,
					region:            "nyc1",
					lbActiveTimeout:   2,
					lbActiveCheckTick: 1,
				}

				var err error
				switch methodName {
				case "EnsureLoadBalancer":
					_, err = lb.EnsureLoadBalancer(context.TODO(), "test", service, nodes)
				case "UpdateLoadBalancer":
					err = lb.UpdateLoadBalancer(context.TODO(), "test", service, nodes)
				default:
					t.Errorf("unsupported loadbalancer method: %s", methodName)
				}

				if !reflect.DeepEqual(err, tc.err) {
					t.Error("error does not match test case expectation")
					t.Logf("expected: %v", tc.err)
					t.Logf("actual: %v", err)
				}

				service, err = fakeResources.kclient.CoreV1().Services(service.Namespace).Get(service.Name, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to get service from fake client: %s", err)
				}

				serviceCertID := getCertificateID(service)
				if tc.expectedServiceCertID != serviceCertID {
					t.Error("unexpected service certificate ID annotation")
					t.Logf("expected: %s", tc.expectedServiceCertID)
					t.Logf("actual: %s", serviceCertID)
				}

				godoLoadBalancer, _, err := lbService.Get(context.TODO(), getLoadBalancerID(service))
				if err != nil {
					t.Fatalf("failed to get loadbalancer %q from fake client: %s", getLoadBalancerID(service), err)
				}
				lbCertID := getCertificateIDFromLB(godoLoadBalancer)
				if tc.expectedLBCertID != lbCertID {
					t.Error("unexpected loadbalancer certificate ID")
					t.Logf("expected: %s", tc.expectedLBCertID)
					t.Logf("actual: %s", lbCertID)
				}
			})
		}
	}
}
