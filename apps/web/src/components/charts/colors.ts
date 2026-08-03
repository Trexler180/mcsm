// Shared chart colors live in a dependency-free module so the tiny live
// sparkline does not pull Recharts into routes that never open history views.
export const C_TPS = "#8b5cf6";

// Upload throughput is a lone series, so it takes the app's accent rather than a
// slot from the categorical order — there is nothing for it to be distinguished
// *from*, and the panel it sits in is already accent-themed.
export const C_UPLOAD = "#22c55e";
