export type Rgba = [number, number, number, number];
export function parseColor(text: string): Rgba;
export function over(fg: Rgba, bg: Rgba): Rgba;
export function luminance(color: Rgba | [number, number, number]): number;
export function ratio(a: Rgba | [number, number, number], b: Rgba | [number, number, number]): number;
export function isLarge(px: number, weight: number): boolean;
