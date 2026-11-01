\* No two clients ever hold the same connection, at every size (#360). The
\* gate writes the module around this proof and the theorem it proves,
\* Spec => []NoSharedConnection, from the three lemmas below, whose
\* statements are the gate's. The inductive invariant only adds TypeOK.

IndInv_NoSharedConnection == TypeOK /\ NoSharedConnection

LEMMA NoSharedConnection_Init == Init => IndInv_NoSharedConnection
BY DEF Init, IndInv_NoSharedConnection, TypeOK, NoSharedConnection

LEMMA NoSharedConnection_Step == IndInv_NoSharedConnection /\ [Next]_vars => IndInv_NoSharedConnection'
BY DEF IndInv_NoSharedConnection, TypeOK, NoSharedConnection, Next, vars, Acquire, Refuse, Release, Out, Conns

LEMMA NoSharedConnection_Implies == IndInv_NoSharedConnection => NoSharedConnection
BY DEF IndInv_NoSharedConnection
