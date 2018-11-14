#!/usr/bin/python
'''
    (C) Copyright 2018 Intel Corporation.

    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.

    GOVERNMENT LICENSE RIGHTS-OPEN SOURCE SOFTWARE
    The Government's rights to use, modify, reproduce, release, perform, display,
    or disclose this software are subject to the terms of the Apache License as
    provided in Contract No. B609815.
    Any reproduction of computer software, computer software documentation, or
    portions thereof marked with this legend must also reproduce the markings.
    '''

import os
import time
import traceback
import sys
import json
import ctypes

from avocado       import Test
from avocado       import main
from avocado.utils import process

sys.path.append('./util')
sys.path.append('../util')
sys.path.append('../../../utils/py')
sys.path.append('./../../utils/py')
import ServerUtils
import WriteHostFile
import daos_api
from daos_api import DaosContext
from daos_api import DaosPool
from daos_api import DaosServer

class PoolSvc(Test):
    """
    Tests svc argument while pool create.

    """
    def setUp(self):
        # get paths from the build_vars generated by build
        with open('../../../.build_vars.json') as f:
            build_paths = json.load(f)
        self.basepath = os.path.normpath(build_paths['PREFIX']  + "/../")
        self.tmp = build_paths['PREFIX'] + '/tmp'

        self.server_group = self.params.get("server_group",'/server/','daos_server')
        self.daosctl = self.basepath + '/install/bin/daosctl'

        # setup the DAOS python API
        self.Context = DaosContext(build_paths['PREFIX'] + '/lib/')
        self.POOL = None

        self.hostfile = None
        self.hostlist = self.params.get("test_machines",'/run/hosts/*')
        self.hostfile = WriteHostFile.WriteHostFile(self.hostlist, self.tmp)
        print("Host file is: {}".format(self.hostfile))

        ServerUtils.runServer(self.hostfile, self.server_group, self.basepath)
        time.sleep(5)

    def tearDown(self):
        if self.hostfile is not None:
            os.remove(self.hostfile)
        if self.POOL is not None and self.POOL.attached:
            self.POOL.destroy(1)

        ServerUtils.stopServer()

    def test_poolsvc(self):
        """
        Test svc arg during pool create.

        :avocado: tags=pool,svc
        """

        # parameters used in pool create
        createmode = self.params.get("mode",'/run/createtests/createmode/*/')
        createuid  = os.geteuid()
        creategid  = os.getegid()
        createsetid = self.params.get("setname",'/run/createtests/createset/')
        createsize  = self.params.get("size",'/run/createtests/createsize/')
        createsvc  = self.params.get("svc",'/run/createtests/createsvc/*/')

        expected_result = createsvc[1]

        try:
            # initialize a python pool object then create the underlying
            # daos storage
            self.POOL = DaosPool(self.Context)
            self.POOL.create(createmode, createuid, creategid,
                    createsize, createsetid, None, None, createsvc[0])
            self.POOL.connect(1 << 1)
            # checking returned rank list value for single server
            if ((len(self.hostlist) == 1) and (int(self.POOL.svc.rl_ranks[i] != 0))):
                self.fail("Incorrect returned rank list value for single server")
            # checking returned rank list for server more than 1
            i = 0
            while ((int(self.POOL.svc.rl_ranks[i]) > 0) and \
                  (int(self.POOL.svc.rl_ranks[i]) <= createsvc[0]) and \
                  (int(self.POOL.svc.rl_ranks[i]) != 999999)):
                i +=1
            if i != createsvc[0]:
                self.fail("Length of Returned Rank list is not equal to" \
                          " the number of Pool Service members.\n")
            list = []
            for j in range(createsvc[0]):
                list.append(int(self.POOL.svc.rl_ranks[j]))
                if len(list) != len(set(list)):
                    self.fail("Duplicate values in returned rank list")

            if (createsvc[0] == 3):
                self.POOL.disconnect()
                cmd = ('{0} kill-leader  --uuid={1}'
                        .format(self.daosctl, self.POOL.get_uuid_str()))
                process.system(cmd)
                self.POOL.connect(1 << 1)
                self.POOL.disconnect()
                server = DaosServer(self.Context, self.server_group, 2)
                server.kill(1)
                self.POOL.exclude([2])
                self.POOL.connect(1 << 1)

            if expected_result in ['FAIL']:
                self.fail("Test was expected to fail but it passed.\n")

        except ValueError as e:
            print e
            print traceback.format_exc()
            if expected_result == 'PASS':
                self.fail("Test was expected to pass but it failed.\n")

