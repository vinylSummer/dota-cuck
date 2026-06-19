from unittest.mock import Mock

import pytest

from grpc_client import CommandDispatcher, UnknownCommand
from spectator.v1 import worker_pb2 as pb


def make_dispatcher():
    handlers = {
        "start": Mock(),
        "stop": Mock(),
        "guard": Mock(),
        "friends": Mock(),
        "link": Mock(),
    }
    dispatcher = CommandDispatcher(
        on_start_spectate=handlers["start"],
        on_stop_spectate=handlers["stop"],
        on_steam_guard=handlers["guard"],
        on_list_friends=handlers["friends"],
        on_link_account=handlers["link"],
    )
    return dispatcher, handlers


def test_start_spectate_routes_with_payload():
    dispatcher, handlers = make_dispatcher()
    cmd = pb.Command(
        start_spectate=pb.StartSpectate(session_id="s1", target_steam_id="76561")
    )

    dispatcher.dispatch(cmd)

    handlers["start"].assert_called_once()
    handlers["stop"].assert_not_called()
    handlers["guard"].assert_not_called()
    assert handlers["start"].call_args.args[0].session_id == "s1"


def test_stop_spectate_routes():
    dispatcher, handlers = make_dispatcher()
    dispatcher.dispatch(pb.Command(stop_spectate=pb.StopSpectate()))
    handlers["stop"].assert_called_once()


def test_steam_guard_routes_with_code():
    dispatcher, handlers = make_dispatcher()
    dispatcher.dispatch(pb.Command(steam_guard=pb.SubmitSteamGuardCode(code="ABCDE")))
    handlers["guard"].assert_called_once()
    assert handlers["guard"].call_args.args[0].code == "ABCDE"


def test_list_friends_routes_with_request_id():
    dispatcher, handlers = make_dispatcher()
    dispatcher.dispatch(
        pb.Command(list_friends=pb.ListFriends(request_id="req-1", refresh_token="rt"))
    )
    handlers["friends"].assert_called_once()
    assert handlers["friends"].call_args.args[0].request_id == "req-1"


def test_link_account_routes_with_request_id():
    dispatcher, handlers = make_dispatcher()
    dispatcher.dispatch(
        pb.Command(link_account=pb.LinkAccount(request_id="req-2", steam_username="u"))
    )
    handlers["link"].assert_called_once()
    assert handlers["link"].call_args.args[0].request_id == "req-2"


def test_empty_command_is_unknown():
    dispatcher, _ = make_dispatcher()
    with pytest.raises(UnknownCommand):
        dispatcher.dispatch(pb.Command())
