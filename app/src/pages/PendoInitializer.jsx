import { useEffect } from 'react';
import { useSession } from 'next-auth/react';
import PropTypes from 'prop-types';

const PendoInitializer = ({ clusterData }) => {
  const { data: session } = useSession();

  useEffect(() => {
    if (clusterData && Object.keys(clusterData).length > 0 && !window.pendo) {
      if (typeof window !== 'undefined' && session) {
        (function (p, e, n, d, o) {
          var v, w, x, y, z;
          o = p[d] = p[d] || {};
          o._q = [];
          v = ['initialize', 'identify', 'updateOptions', 'pageLoad'];
          for (w = 0, x = v.length; w < x; ++w) {
            (function (m) {
              o[m] =
                o[m] ||
                function () {
                  o._q[m === v[0] ? 'unshift' : 'push']([m].concat([].slice.call(arguments, 0)));
                };
            })(v[w]);
          }
          y = e.createElement(n);
          y.async = !0;
          y.src = 'https://cdn.pendo.io/agent/static/6577de72-8242-45dd-6766-29d298b67059/pendo.js';
          z = e.getElementsByTagName(n)[0];
          z.parentNode.insertBefore(y, z);
        })(window, document, 'script', 'pendo');

        window.pendo.initialize({
          visitor: {
            id: session.user.email,
            email: session.user.email,
            name: session.user.name,
          },
          account: {
            id: clusterData.value,
            name: clusterData.label,
            type: clusterData.cloud_provider,
            tenantId: session.tenant.name,
            tenantName: session.tenant.name,
          },
        });
      }
    } else if (
      typeof window !== 'undefined' &&
      window.pendo &&
      clusterData &&
      Object.keys(clusterData).length > 0 &&
      typeof window.pendo.getSerializedMetadata === 'function'
    ) {
      const currentOptions = window.pendo.getSerializedMetadata();
      const updatedAccount = {
        ...currentOptions.account,
        id: clusterData.value,
        name: clusterData.cluster_name,
      };
      window.pendo.updateOptions({
        ...currentOptions,
        account: updatedAccount,
      });
    }
  }, [clusterData]);

  return null;
};

PendoInitializer.propTypes = {
  clusterData: PropTypes.object.isRequired,
};

export default PendoInitializer;
