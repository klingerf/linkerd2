use crate::routes::{
    FailureInjectorFilter, GroupKindNamespaceName, HeaderModifierFilter, HostMatch, HttpRouteMatch,
    RequestRedirectFilter,
};
use ahash::AHashMap as HashMap;
use anyhow::Result;
use chrono::{offset::Utc, DateTime};
use futures::prelude::*;
use std::{net::IpAddr, num::NonZeroU16, pin::Pin, time};

/// Models outbound policy discovery.
#[async_trait::async_trait]
pub trait DiscoverOutboundPolicy<T> {
    async fn get_outbound_policy(&self, target: T) -> Result<Option<OutboundPolicy>>;

    async fn watch_outbound_policy(&self, target: T) -> Result<Option<OutboundPolicyStream>>;

    fn lookup_ip(&self, addr: IpAddr, port: NonZeroU16, source_namespace: String) -> Option<T>;
}

pub type OutboundPolicyStream = Pin<Box<dyn Stream<Item = OutboundPolicy> + Send + Sync + 'static>>;

pub struct OutboundDiscoverTarget {
    pub service_name: String,
    pub service_namespace: String,
    pub service_port: NonZeroU16,
    pub source_namespace: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum TypedOutboundRoute {
    Http(OutboundRoute<HttpRouteMatch>),
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub enum OutboundRouteCollection {
    #[default]
    Empty,
    Http(HashMap<GroupKindNamespaceName, OutboundRoute<HttpRouteMatch>>),
}

#[derive(Clone, Debug, PartialEq)]
pub struct OutboundPolicy {
    pub routes: OutboundRouteCollection,
    pub authority: String,
    pub name: String,
    pub namespace: String,
    pub port: NonZeroU16,
    pub opaque: bool,
    pub accrual: Option<FailureAccrual>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OutboundRoute<MatchType> {
    pub hostnames: Vec<HostMatch>,
    pub rules: Vec<OutboundRouteRule<MatchType>>,

    /// This is required for ordering returned routes
    /// by their creation timestamp.
    pub creation_timestamp: Option<DateTime<Utc>>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OutboundRouteRule<MatchType> {
    pub matches: Vec<MatchType>,
    pub backends: Vec<Backend>,
    pub request_timeout: Option<time::Duration>,
    pub backend_request_timeout: Option<time::Duration>,
    pub filters: Vec<Filter>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Backend {
    Addr(WeightedAddr),
    Service(WeightedService),
    Invalid { weight: u32, message: String },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WeightedAddr {
    pub weight: u32,
    pub addr: IpAddr,
    pub port: NonZeroU16,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WeightedService {
    pub weight: u32,
    pub authority: String,
    pub name: String,
    pub namespace: String,
    pub port: NonZeroU16,
    pub filters: Vec<Filter>,
    pub exists: bool,
}

#[derive(Copy, Clone, Debug, PartialEq)]
pub enum FailureAccrual {
    Consecutive { max_failures: u32, backoff: Backoff },
}

#[derive(Copy, Clone, Debug, PartialEq)]
pub struct Backoff {
    pub min_penalty: time::Duration,
    pub max_penalty: time::Duration,
    pub jitter: f32,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Filter {
    RequestHeaderModifier(HeaderModifierFilter),
    ResponseHeaderModifier(HeaderModifierFilter),
    RequestRedirect(RequestRedirectFilter),
    FailureInjector(FailureInjectorFilter),
}

// === impl TypedOutboundRoute ===

impl From<OutboundRoute<HttpRouteMatch>> for TypedOutboundRoute {
    fn from(route: OutboundRoute<HttpRouteMatch>) -> Self {
        Self::Http(route)
    }
}

// === impl OutboundRouteCollection ===

impl OutboundRouteCollection {
    pub fn is_empty(&self) -> bool {
        matches!(self, Self::Empty)
    }

    pub fn remove(&mut self, key: &GroupKindNamespaceName) {
        match self {
            Self::Empty => {}
            Self::Http(routes) => {
                routes.remove(key);
                if routes.is_empty() {
                    *self = Self::Empty;
                }
            }
        }
    }

    pub fn insert<Route: Into<TypedOutboundRoute>>(
        &mut self,
        key: GroupKindNamespaceName,
        route: Route,
    ) -> Result<Option<TypedOutboundRoute>> {
        let route = route.into();

        match (self, route) {
            (this @ Self::Empty, TypedOutboundRoute::Http(route)) => {
                let mut routes = HashMap::default();
                let inserted = routes.insert(key, route).map(Into::into);
                *this = Self::Http(routes);
                Ok(inserted)
            }
            (Self::Http(routes), TypedOutboundRoute::Http(route)) => {
                Ok(routes.insert(key, route).map(Into::into))
            }
        }
    }
}
