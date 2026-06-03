export const applyFiltersOnRouter = (router: any, param: any) => {
  const { ...otherParams } = router.query;
  const updatedParams = {
    ...otherParams,
    ...param,
  };
  Object.keys(updatedParams).forEach((key) => {
    if (updatedParams[key] === '' || !updatedParams[key]) {
      delete updatedParams[key];
    }
  });

  const hash = router.asPath.split('#');
  router.push({
    pathname: router.pathname,
    query: updatedParams,
    hash: hash[1],
  });
};
