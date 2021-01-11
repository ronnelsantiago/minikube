/*
Copyright 2019 The Kubernetes Authors All rights reserved.

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

package kubeconfig

import (
	"io/ioutil"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/juju/mutex"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
	"k8s.io/minikube/pkg/util/lock"
)

// Settings is the minikubes settings for kubeconfig
type Settings struct {
	// The name of the cluster for this context
	ClusterName string

	// The name of the namespace for this context
	Namespace string

	// ClusterServerAddress is the address of the Kubernetes cluster
	ClusterServerAddress string

	// ClientCertificate is the path to a client cert file for TLS.
	ClientCertificate string

	// CertificateAuthority is the path to a cert file for the certificate authority.
	CertificateAuthority string

	// ClientKey is the path to a client key file for TLS.
	ClientKey string

	// Should the current context be kept when setting up this one
	KeepContext bool

	// Should the certificate files be embedded instead of referenced by path
	EmbedCerts bool

	// kubeConfigFile is the path where the kube config is stored
	// Only access this with atomic ops
	kubeConfigFile atomic.Value
}

// SetPath sets the setting for kubeconfig filepath
func (k *Settings) SetPath(kubeConfigFile string) {
	k.kubeConfigFile.Store(kubeConfigFile)
}

// filePath gets the kubeconfig file
func (k *Settings) filePath() string {
	return k.kubeConfigFile.Load().(string)
}

// PopulateFromSettings populates an api.Config object with values from *Settings
func PopulateFromSettings(cfg *Settings, apiCfg *api.Config) error {
	var err error
	clusterName := cfg.ClusterName
	cluster := api.NewCluster()
	cluster.Server = cfg.ClusterServerAddress
	if cfg.EmbedCerts {
		cluster.CertificateAuthorityData, err = ioutil.ReadFile(cfg.CertificateAuthority)
		if err != nil {
			return errors.Wrapf(err, "reading CertificateAuthority %s", cfg.CertificateAuthority)
		}
	} else {
		cluster.CertificateAuthority = cfg.CertificateAuthority
	}

	lastUpdate := time.Now().String()
	ext := &internalExtension{
		CreatedBy:  "minikube.sigs.k8s.io",
		LastUpdate: lastUpdate,
	}

	cluster.Extensions = map[string]runtime.Object{"cluster_info": ext.DeepCopy()}
	apiCfg.Clusters[clusterName] = cluster

	// user
	userName := cfg.ClusterName
	user := api.NewAuthInfo()
	if cfg.EmbedCerts {
		user.ClientCertificateData, err = ioutil.ReadFile(cfg.ClientCertificate)
		if err != nil {
			return errors.Wrapf(err, "reading ClientCertificate %s", cfg.ClientCertificate)
		}
		user.ClientKeyData, err = ioutil.ReadFile(cfg.ClientKey)
		if err != nil {
			return errors.Wrapf(err, "reading ClientKey %s", cfg.ClientKey)
		}
	} else {
		user.ClientCertificate = cfg.ClientCertificate
		user.ClientKey = cfg.ClientKey
	}
	apiCfg.AuthInfos[userName] = user

	// context
	contextName := cfg.ClusterName
	context := api.NewContext()
	context.Cluster = cfg.ClusterName
	context.Namespace = cfg.Namespace
	context.AuthInfo = userName
	context.Extensions = map[string]runtime.Object{"context_info": ext.DeepCopy()}
	apiCfg.Contexts[contextName] = context

	// Only set current context to minikube if the user has not used the keepContext flag
	if !cfg.KeepContext {
		apiCfg.CurrentContext = cfg.ClusterName
	}

	return nil
}

// Update reads config from disk, adds the minikube settings, and writes it back.
// activeContext is true when minikube is the CurrentContext
// If no CurrentContext is set, the given name will be used.
func Update(kcs *Settings) error {
	spec := lock.PathMutexSpec(filepath.Join(kcs.filePath(), "settings.Update"))
	klog.Infof("acquiring lock: %+v", spec)
	releaser, err := mutex.Acquire(spec)
	if err != nil {
		return errors.Wrapf(err, "unable to acquire lock for %+v", spec)
	}
	defer releaser.Release()

	// read existing config or create new if does not exist
	klog.Infoln("Updating kubeconfig: ", kcs.filePath())
	kcfg, err := readOrNew(kcs.filePath())
	if err != nil {
		return err
	}

	err = PopulateFromSettings(kcs, kcfg)
	if err != nil {
		return err
	}

	// write back to disk
	if err := writeToFile(kcfg, kcs.filePath()); err != nil {
		return errors.Wrap(err, "writing kubeconfig")
	}
	return nil
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// implementing the runtime.Object internally so we can write extensions to kubeconfig
type internalExtension struct {
	runtime.TypeMeta `json:",inline"`
	CreatedBy        string `json:"created_by"`
	LastUpdate       string `json:"last_update"`
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new InternalSimple.
func (in *internalExtension) DeepCopy() *internalExtension {
	if in == nil {
		return nil
	}
	out := new(internalExtension)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *internalExtension) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *internalExtension) DeepCopyInto(out *internalExtension) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	return
}
