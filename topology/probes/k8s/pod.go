/*
 * Copyright (C) 2017 Red Hat, Inc.
 *
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 *
 */

package k8s

import (
	"sync"

	"github.com/skydive-project/skydive/logging"
	"github.com/skydive-project/skydive/topology"
	"github.com/skydive-project/skydive/topology/graph"

	api "k8s.io/api/core/v1"
)

type podCache struct {
	sync.RWMutex
	defaultKubeCacheEventHandler
	graph.DefaultGraphListener
	*kubeCache
	graph            *graph.Graph
	containerIndexer *graph.MetadataIndexer
	hostIndexer      *graph.MetadataIndexer
}

func newPodIndex(g *graph.Graph, by string) *graph.MetadataIndexer {
	return graph.NewMetadataIndexer(g, graph.Metadata{"Type": "pod"}, by)
}

func newPodIndexerByHost(g *graph.Graph) *graph.MetadataIndexer {
	return newPodIndex(g, "Pod.NodeName")
}

func newPodIndexerByNamespace(g *graph.Graph) *graph.MetadataIndexer {
	return newPodIndex(g, "Pod.Namespace")
}

func newPodIndexerByName(g *graph.Graph) *graph.MetadataIndexer {
	return newPodIndex(g, "Name")
}

func (p *podCache) newMetadata(pod *api.Pod) graph.Metadata {
	return newMetadata("pod", pod.GetName(), pod)
}

func (p *podCache) linkPodToHost(pod *api.Pod, podNode *graph.Node) {
	hostNodes := p.hostIndexer.Get(pod.Spec.NodeName)
	if len(hostNodes) == 0 {
		return
	}
	linkPodToHost(p.graph, hostNodes[0], podNode)
}

func (p *podCache) onAdd(obj interface{}) {
	pod, ok := obj.(*api.Pod)
	if !ok {
		return
	}

	podNode := p.graph.NewNode(graph.Identifier(pod.GetUID()), p.newMetadata(pod))

	containerNodes := p.containerIndexer.Get(pod.Namespace, pod.Name)
	for _, containerNode := range containerNodes {
		p.graph.Link(podNode, containerNode, podToContainerMetadata)
	}

	p.linkPodToHost(pod, podNode)
}

func (p *podCache) OnAdd(obj interface{}) {
	pod, ok := obj.(*api.Pod)
	if !ok {
		return
	}

	p.Lock()
	defer p.Unlock()

	p.graph.Lock()
	defer p.graph.Unlock()

	logging.GetLogger().Infof("Creating node for pod{%s}", pod.GetName())

	p.onAdd(obj)
}

func (p *podCache) OnUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*api.Pod)
	newPod := newObj.(*api.Pod)

	p.Lock()
	defer p.Unlock()

	p.graph.Lock()
	defer p.graph.Unlock()

	podNode := p.graph.GetNode(graph.Identifier(newPod.GetUID()))
	if podNode == nil {
		logging.GetLogger().Infof("Updating (re-adding) node for pod{%s}", newPod.GetName())
		p.onAdd(newObj)
		return
	}

	logging.GetLogger().Infof("Updating node for pod{%s}", newPod.GetName())
	if oldPod.Spec.NodeName == "" && newPod.Spec.NodeName != "" {
		p.linkPodToHost(newPod, podNode)
	}

	addMetadata(p.graph, podNode, newPod)
}

func (p *podCache) OnDelete(obj interface{}) {
	if pod, ok := obj.(*api.Pod); ok {
		logging.GetLogger().Infof("Deleting node for pod{%s}", pod.GetName())
		p.graph.Lock()
		if podNode := p.graph.GetNode(graph.Identifier(pod.GetUID())); podNode != nil {
			p.graph.DelNode(podNode)
		}
		p.graph.Unlock()
	}
}

func linkPodsToHost(g *graph.Graph, host *graph.Node, pods []*graph.Node) {
	for _, pod := range pods {
		linkPodToHost(g, host, pod)
	}
}

func linkPodToHost(g *graph.Graph, host, pod *graph.Node) {
	topology.AddOwnershipLink(g, host, pod, nil)
}

func (p *podCache) List() (pods []*api.Pod) {
	for _, pod := range p.cache.List() {
		pods = append(pods, pod.(*api.Pod))
	}
	return
}

func (p *podCache) GetByKey(key string) *api.Pod {
	if pod, found, _ := p.cache.GetByKey(key); found {
		return pod.(*api.Pod)
	}
	return nil
}

func (p *podCache) Start() {
	p.containerIndexer.AddEventListener(p)
	p.hostIndexer.AddEventListener(p)
	p.kubeCache.Start()
}

func (p *podCache) Stop() {
	p.containerIndexer.RemoveEventListener(p)
	p.hostIndexer.RemoveEventListener(p)
	p.kubeCache.Stop()
}

func newPodCache(client *kubeClient, g *graph.Graph) *podCache {
	p := &podCache{
		graph:            g,
		containerIndexer: newContainerIndexer(g),
		hostIndexer:      newHostIndexer(g),
	}
	p.kubeCache = client.getCacheFor(client.Core().RESTClient(), &api.Pod{}, "pods", p)
	return p
}
