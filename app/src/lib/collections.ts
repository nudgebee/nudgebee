export function unique(data: any[], key?: string): any[] {
  if (key) {
    data = data.map((item) => item[key]);
  }
  const data2 = new Set(data);
  return Array.from(data2);
}

export function select(data: any[], key: string): any[] {
  data = data.map((item) => item[key]);
  return data;
}

export function objectToArray(obj: any, keyPrefix?: string): any[][] {
  keyPrefix = keyPrefix || '';
  return Object.keys(obj).flatMap((k) => {
    const key = keyPrefix + k;
    if (typeof obj[key] === 'object') {
      return objectToArray(obj[key], key + '.');
    }
    return [[key, obj[k]]];
  });
}

export function sum(data: any[], key?: string, optional = 0): number {
  if (!data) {
    return optional;
  }
  return data.reduce((acc, item) => {
    if (key) {
      return acc + item[key];
    }
    return acc + item;
  }, 0);
}
