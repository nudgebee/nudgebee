class CacheObject {
  value: any;
  expires: number;
  constructor(value: any, ttl: number) {
    this.value = value;
    if (ttl === 0) {
      ttl = 1000 * 60 * 60 * 24 * 365;
    }
    this.expires = Date.now() + ttl;
  }
}

function getHashCode(obj: any) {
  let str = '';
  if (typeof obj === 'string') {
    str = obj;
  } else if (typeof obj === 'number') {
    str = obj.toString();
  } else {
    const keys = Object.keys(obj).sort();
    for (const key of keys) {
      str += `${key}:${obj[key]}`;
    }
  }
  //get hash
  const hashCode = str.split('').reduce((a, b) => ((a << 5) - a + b.charCodeAt(0)) | 0, 0);
  return hashCode;
}

const cache = new Map<string, CacheObject>();

export function set(key: string, value: any, ttlSec = 0) {
  cache.set(key, new CacheObject(value, ttlSec * 1000));
}

export function setWithSuffix(key: string, value: any, keySuffix: any = null, ttlSec = 0) {
  key = `${key}_${getHashCode(keySuffix)}`;
  set(key, value, ttlSec);
}

export function get(key: string, optional: any = null): any {
  const obj = cache.get(key);
  if (!obj) {
    return optional;
  }
  if (obj.expires < Date.now()) {
    cache.delete(key);
    return optional;
  }
  return obj.value;
}

export function getWithSuffix(key: string, optional: any = null, keySuffix: any = null): any {
  key = `${key}_${getHashCode(keySuffix)}`;
  return get(key, optional);
}

export function del(key: string) {
  cache.delete(key);
}

export function delWithSuffix(key: string) {
  cache.forEach((value, k) => {
    if (k.includes(key)) {
      cache.delete(k);
    }
  });
}

export function clear() {
  cache.clear();
}

const inmemoryCache = {
  set: set,
  get: get,
  setWithSuffix: setWithSuffix,
  getWithSuffix: getWithSuffix,
  del: del,
  delWithSuffix: delWithSuffix,
  clear: clear,
};

export default inmemoryCache;
