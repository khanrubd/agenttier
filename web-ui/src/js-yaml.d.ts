// Copyright 2024 AgentTier Authors.
// SPDX-License-Identifier: Apache-2.0

declare module 'js-yaml' {
  export function dump(obj: any, opts?: any): string;
  export function load(str: string, opts?: any): any;
}
