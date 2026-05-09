declare module 'js-yaml' {
  export function dump(obj: any, opts?: any): string;
  export function load(str: string, opts?: any): any;
}
